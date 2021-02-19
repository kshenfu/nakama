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
// 实时接收 timelineitem，并进行消费。
func (s *Service) TimelineItemStream(ctx context.Context) (<-chan TimelineItem, error) {
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return nil, ErrUnauthenticated
	}

	tt := make(chan TimelineItem)
	// 从 NAT MQ中消费接收到的TimelineItem，是对于这个Topic的所有消息都调用 sub() 函数的第二个参数进行处理吗？
	// TimelineItemStream 这是用户登录上来的时候需要调用的函数，用来获取这个用户所有的通知。
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
		<-ctx.Done() // 如果父goroutine 发起了取消请求，需要进行清理。
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
// 关于fanout的含义，参考: https://mp.weixin.qq.com/s?__biz=MjM5NzQ3ODAwMQ==&mid=404465806&idx=1&sn=3a68a786138538ffc452bca06a4892c8&scene=0#rd
// fanout表示广播模式，当用户发布新帖子的时候，需要通知所有关注这个用户的粉丝
func (s *Service) fanoutPost(p Post) {
	// 首先插入 timeline 表，这个表记录了userid,post_id，并且生成了一个 id字段。这个表的作用是用来给关注的用户进行通知用的
	// 所有关注的用户会订阅 timelineTopic() 生成的topic，然后发了新帖子的时候，会给这个 topic 生产一个消息，然后供
	// 订阅这个 topic 的用户去进行消费。
	// INSERT INTO 语句后加上 SELECT 语句，表示这条INSERT语句中的value里的个别值需要从SELECT 查询的表中获取。
	// 这里的SQL 语句含义是：从follows 中查出followee_id=p.UserID 的记录中的 follower_id ，再加上p.ID 作为 INSERT 语句的value。
	// 这里有点疑惑的是：p.ID（也就是postid）根本不是 follows 表中的字段，假设 p.ID = 112233445566，那么久变成了：
	// SELECT follower_id, 112233445566 FROM follows WHERE followee_id = $2，SELECT 语句后跟一个常量而不是一个字段名，这样的方式是百分百能够返回 follower_id 的值加上112233445566吗？
	// 如果是这样的话，这两个值刚好组成 (user_id, post_id) 的值，可以插入到 timeline 表中。
	// 所以，这个sql 的意思是：将所有关注 p.UserID 的用户，以及这个postid 组成一行数据插入到 timeline 表中，然后返回插入timeline表后自动生成的 id 和 user_id，
	// 返回的每一条结果代表一条需要通知的消息。
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

		go s.broadcastTimelineItem(ti) //通知所有关注这个用户的粉丝，这些放到后台通知，对粉丝的通知没必要等待，尽快响应。
	}

	if err = rows.Err(); err != nil {
		log.Printf("could not iterate timeline rows: %v\n", err)
		return
	}
}

//广播一条 TimelineItem，一条 TimelineItem 表示一个通知。
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

// 对于关注userID 这个用户的所有粉丝进行通知的消息的topic 命名为 "timeline_item_" + userID
func timelineTopic(userID string) string { return "timeline_item_" + userID }
