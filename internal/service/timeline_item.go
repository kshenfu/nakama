package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
)

// ErrInvalidTimelineItemID denotes an invalid timeline item id; that is not uuid.
var ErrInvalidTimelineItemID = errors.New("invalid timeline item id")

// TimelineItem model.
type TimelineItem struct {
	ID     string `json:"id"`
	UserID string `json:"-"`
	PostID string `json:"-"`
	Post   *Post  `json:"post,omitempty"`
}

// Timeline of the authenticated user in descending order and with backward pagination.
// 将帖子按发布时间排序进行返回
func (s *Service) Timeline(ctx context.Context, last int, before string) ([]TimelineItem, error) {
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return nil, ErrUnauthenticated
	}

	last = normalizePageSize(last)
	// 按创建时间递减进行排序，这样最新创建的帖子就在最前面
	query, args, err := buildQuery(`
		SELECT timeline.id, posts.id, content, spoiler_of, nsfw, likes_count, comments_count, created_at
		, posts.user_id = @uid AS mine
		, likes.user_id IS NOT NULL AS liked
		, subscriptions.user_id IS NOT NULL AS subscribed
		, users.username, users.avatar
		FROM timeline
		INNER JOIN posts ON timeline.post_id = posts.id
		INNER JOIN users ON posts.user_id = users.id
		LEFT JOIN post_likes AS likes
			ON likes.user_id = @uid AND likes.post_id = posts.id
		LEFT JOIN post_subscriptions AS subscriptions
			ON subscriptions.user_id = @uid AND subscriptions.post_id = posts.id
		WHERE timeline.user_id = @uid
		{{if .before}}AND timeline.id < @before{{end}}
		ORDER BY created_at DESC
		LIMIT @last`, map[string]interface{}{
		"uid":    uid,
		"last":   last,
		"before": before,
	})
	if err != nil {
		return nil, fmt.Errorf("could not build timeline sql query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not query select timeline: %w", err)
	}

	defer rows.Close()

	tt := make([]TimelineItem, 0, last)
	for rows.Next() {
		var ti TimelineItem
		var p Post
		var u User
		var avatar sql.NullString
		if err = rows.Scan(
			&ti.ID,
			&p.ID,
			&p.Content,
			&p.SpoilerOf,
			&p.NSFW,
			&p.LikesCount,
			&p.CommentsCount,
			&p.CreatedAt,
			&p.Mine,
			&p.Liked,
			&p.Subscribed,
			&u.Username,
			&avatar,
		); err != nil {
			return nil, fmt.Errorf("could not scan timeline item: %w", err)
		}

		u.AvatarURL = s.avatarURL(avatar)
		p.User = &u
		ti.Post = &p
		tt = append(tt, ti)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("could not iterate timeline rows: %w", err)
	}

	return tt, nil
}

// TimelineItemStream to receive timeline items in realtime.
func (s *Service) TimelineItemStream(ctx context.Context) (<-chan TimelineItem, error) {
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return nil, ErrUnauthenticated
	}

	tt := make(chan TimelineItem)
	unsub, err := s.pubsub.Sub(timelineTopic(uid), func(data []byte) {
		go func(r io.Reader) {
			var ti TimelineItem
			err := gob.NewDecoder(r).Decode(&ti)
			if err != nil {
				log.Printf("could not gob decode timeline item: %v\n", err)
				return
			}

			tt <- ti
		}(bytes.NewReader(data))
	})
	if err != nil {
		return nil, fmt.Errorf("could not subscribe to timeline: %w", err)
	}

	go func() {
		<-ctx.Done()
		if err := unsub(); err != nil {
			log.Printf("could not unsubcribe from timeline: %v\n", err)
			// don't return
		}

		close(tt)
	}()

	return tt, nil
}

// DeleteTimelineItem from the auth user timeline.
func (s *Service) DeleteTimelineItem(ctx context.Context, timelineItemID string) error {
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return ErrUnauthenticated
	}

	if !reUUID.MatchString(timelineItemID) {
		return ErrInvalidTimelineItemID
	}

	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM timeline
		WHERE id = $1 AND user_id = $2`, timelineItemID, uid); err != nil {
		return fmt.Errorf("could not delete timeline item: %w", err)
	}

	return nil
}

// 更新 timeline 表，这个表的用处是什么现在还不知道
func (s *Service) fanoutPost(p Post) {
	// 首先插入 timeline 表，这个表记录了userid,post_id，并且生成了一个 id字段。这个表的作用是用来给关注的用户进行通知用的
	// 所有关注的用户会订阅 timelineTopic() 生成的topic，然后发了新帖子的时候，会给这个 topic 生产一个消息，然后供
	// 订阅这个 topic 的用户去进行消费。
	query := `
		INSERT INTO timeline (user_id, post_id)
		SELECT follower_id, $1 FROM follows WHERE followee_id = $2
		RETURNING id, user_id`
	rows, err := s.db.Query(query, p.ID, p.UserID)
	if err != nil {
		log.Printf("could not insert timeline: %v\n", err)
		return
	}

	defer rows.Close()

	for rows.Next() {
		var ti TimelineItem
		if err = rows.Scan(&ti.ID, &ti.UserID); err != nil {
			log.Printf("could not scan timeline item: %v\n", err)
			return
		}

		ti.PostID = p.ID
		ti.Post = &p

		go s.broadcastTimelineItem(ti)
	}

	if err = rows.Err(); err != nil {
		log.Printf("could not iterate timeline rows: %v\n", err)
		return
	}
}

func (s *Service) broadcastTimelineItem(ti TimelineItem) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(ti)
	if err != nil {
		log.Printf("could not gob encode timeline item: %v\n", err)
		return
	}

	// 这个Pub() 是发布一个 Topic，还是往topic里面添加了一个消息供消费者消费呢？
	err = s.pubsub.Pub(timelineTopic(ti.UserID), b.Bytes())
	if err != nil {
		log.Printf("could not publish timeline item: %v\n", err)
		return
	}
}

func timelineTopic(userID string) string { return "timeline_item_" + userID }
