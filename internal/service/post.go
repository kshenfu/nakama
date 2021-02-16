package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/cockroach-go/crdb"
)

var (
	// ErrInvalidPostID denotes an invalid post id; that is not uuid.
	ErrInvalidPostID = errors.New("invalid post id")
	// ErrInvalidContent denotes an invalid content.
	// 如果是表情的话，会返回这个
	ErrInvalidContent = errors.New("invalid content")
	// ErrInvalidSpoiler denotes an invalid spoiler title.
	ErrInvalidSpoiler = errors.New("invalid spoiler")
	// ErrPostNotFound denotes a not found post.
	ErrPostNotFound = errors.New("post not found")
)

// Post model.
type Post struct {
	ID            string    `json:"id"`
	UserID        string    `json:"-"`
	Content       string    `json:"content"`
	SpoilerOf     *string   `json:"spoilerOf"`
	NSFW          bool      `json:"NSFW"`          //是否有安全警告，有些帖子会被标注为不安全的帖子
	LikesCount    int       `json:"likesCount"`    //点赞数
	CommentsCount int       `json:"commentsCount"` // 评论数
	CreatedAt     time.Time `json:"createdAt"`     // 帖子发布时间
	User          *User     `json:"user,omitempty"`
	Mine          bool      `json:"mine"`       // 是否是当前用户自己发的帖子
	Liked         bool      `json:"liked"`      // 当前用户是否点赞了这个帖子
	Subscribed    bool      `json:"subscribed"` // 当前用户是否订阅了这个帖子（也可以说是收藏）
}

// ToggleLikeOutput response.
type ToggleLikeOutput struct {
	Liked      bool `json:"liked"`
	LikesCount int  `json:"likesCount"`
}

// ToggleSubscriptionOutput response.
type ToggleSubscriptionOutput struct {
	Subscribed bool `json:"subscribed"`
}

// CreatePost publishes a post to the user timeline and fan-outs it to his followers.
func (s *Service) CreatePost(ctx context.Context, content string, spoilerOf *string, nsfw bool) (TimelineItem, error) {
	var ti TimelineItem
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return ti, ErrUnauthenticated
	}

	content = smartTrim(content)
	if content == "" || utf8.RuneCountInString(content) > 480 { //emoji表情符号的长度不正确
		return ti, ErrInvalidContent
	}

	if spoilerOf != nil {
		*spoilerOf = smartTrim(*spoilerOf)
		if *spoilerOf == "" || utf8.RuneCountInString(*spoilerOf) > 64 {
			return ti, ErrInvalidSpoiler
		}
	}

	var p Post
	err := crdb.ExecuteTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		// 这个sql表示如果插入成功返回id和 created_at 2个字段。
		query := `
			INSERT INTO posts (user_id, content, spoiler_of, nsfw) VALUES ($1, $2, $3, $4)
			RETURNING id, created_at`
		row := tx.QueryRowContext(ctx, query, uid, content, spoilerOf, nsfw)
		err := row.Scan(&p.ID, &p.CreatedAt)
		if err != nil {
			return fmt.Errorf("could not insert post: %w", err)
		}

		p.UserID = uid
		p.Content = content
		p.SpoilerOf = spoilerOf
		p.NSFW = nsfw
		p.Mine = true

		query = "INSERT INTO post_subscriptions (user_id, post_id) VALUES ($1, $2)"
		if _, err = tx.ExecContext(ctx, query, uid, p.ID); err != nil {
			return fmt.Errorf("could not insert post subscription: %w", err)
		}

		p.Subscribed = true
		// 插入时间轴，一些应用上会有这样的提示: 这个帖子发布于多少分钟前，或者1小时前（比如微博）
		query = "INSERT INTO timeline (user_id, post_id) VALUES ($1, $2) RETURNING id"
		err = tx.QueryRowContext(ctx, query, uid, p.ID).Scan(&ti.ID)
		if err != nil {
			return fmt.Errorf("could not insert timeline item: %w", err)
		}

		ti.UserID = uid
		ti.PostID = p.ID
		ti.Post = &p

		return nil
	})
	if err != nil {
		return ti, err
	}

	go s.postCreated(p) // 这些操作不需要返回给客户端，应该放到协程中去做，而且可以加快响应速度。

	return ti, nil
}

// 帖子发出后通知关注我的用户，并且设置通知，当有人@我时会给我通知。
func (s *Service) postCreated(p Post) {
	// 查询出用户的信息，用来填充到Post中
	u, err := s.userByID(context.Background(), p.UserID)
	if err != nil {
		log.Printf("could not fetch post user: %v\n", err)
		return
	}

	p.User = &u
	p.Mine = false
	p.Subscribed = false

	go s.fanoutPost(p)
	go s.notifyPostMention(p)
}

// Posts from a user in descending order and with backward pagination.
// 根据用户名获取到一个用户发表的所有帖子
func (s *Service) Posts(ctx context.Context, username string, last int, before string) ([]Post, error) {
	username = strings.TrimSpace(username)
	if !reUsername.MatchString(username) {
		return nil, ErrInvalidUsername
	}

	if before != "" && !reUUID.MatchString(before) {
		return nil, ErrInvalidPostID
	}

	uid, auth := ctx.Value(KeyAuthUserID).(string)
	last = normalizePageSize(last)
	query, args, err := buildQuery(`
		SELECT id, content, spoiler_of, nsfw, likes_count, comments_count, created_at
		{{if .auth}}
		, posts.user_id = @uid AS mine
		, likes.user_id IS NOT NULL AS liked
		, subscriptions.user_id IS NOT NULL AS subscribed
		{{end}}
		FROM posts
		{{if .auth}}
		LEFT JOIN post_likes AS likes
			ON likes.user_id = @uid AND likes.post_id = posts.id
		LEFT JOIN post_subscriptions AS subscriptions
			ON subscriptions.user_id = @uid AND subscriptions.post_id = posts.id
		{{end}}
		WHERE posts.user_id = (SELECT id FROM users WHERE username = @username)
		{{if .before}}AND posts.id < @before{{end}}
		ORDER BY created_at DESC
		LIMIT @last`, map[string]interface{}{
		"auth":     auth,
		"uid":      uid,
		"username": username,
		"last":     last,
		"before":   before,
	})
	if err != nil {
		return nil, fmt.Errorf("could not build posts sql query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not query select posts: %w", err)
	}

	defer rows.Close()

	pp := make([]Post, 0, last)
	for rows.Next() {
		var p Post
		dest := []interface{}{
			&p.ID,
			&p.Content,
			&p.SpoilerOf,
			&p.NSFW,
			&p.LikesCount,
			&p.CommentsCount,
			&p.CreatedAt,
		}
		if auth {
			dest = append(dest, &p.Mine, &p.Liked, &p.Subscribed)
		}

		if err = rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("could not scan post: %w", err)
		}

		pp = append(pp, p)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("could not iterate posts rows: %w", err)
	}

	return pp, nil
}

// Post with the given ID.
// 根据PostId 获取到帖子的详细信息，以及当前用户是否对这个帖子点赞收藏这些状态信息
func (s *Service) Post(ctx context.Context, postID string) (Post, error) {
	var p Post
	if !reUUID.MatchString(postID) {
		return p, ErrInvalidPostID
	}

	uid, auth := ctx.Value(KeyAuthUserID).(string)
	query, args, err := buildQuery(`
		SELECT posts.id, content, spoiler_of, nsfw, likes_count, comments_count, created_at
		, users.username, users.avatar
		{{if .auth}}
		, posts.user_id = @uid AS mine
		, likes.user_id IS NOT NULL AS liked
		, subscriptions.user_id IS NOT NULL AS subscribed
		{{end}}
		FROM posts
		INNER JOIN users ON posts.user_id = users.id
		{{if .auth}}
		LEFT JOIN post_likes AS likes
			ON likes.user_id = @uid AND likes.post_id = posts.id
		LEFT JOIN post_subscriptions AS subscriptions
			ON subscriptions.user_id = @uid AND subscriptions.post_id = posts.id
		{{end}}
		WHERE posts.id = @post_id`, map[string]interface{}{
		"auth":    auth,
		"uid":     uid,
		"post_id": postID,
	})
	if err != nil {
		return p, fmt.Errorf("could not build post sql query: %w", err)
	}

	var u User
	var avatar sql.NullString
	dest := []interface{}{
		&p.ID,
		&p.Content,
		&p.SpoilerOf,
		&p.NSFW,
		&p.LikesCount,
		&p.CommentsCount,
		&p.CreatedAt,
		&u.Username,
		&avatar,
	}
	if auth {
		dest = append(dest, &p.Mine, &p.Liked, &p.Subscribed)
	}
	err = s.db.QueryRowContext(ctx, query, args...).Scan(dest...)
	if err == sql.ErrNoRows {
		return p, ErrPostNotFound
	}

	if err != nil {
		return p, fmt.Errorf("could not query select post: %w", err)
	}

	u.AvatarURL = s.avatarURL(avatar)
	p.User = &u

	return p, nil
}

// TogglePostLike 🖤
// 给帖子点赞
func (s *Service) TogglePostLike(ctx context.Context, postID string) (ToggleLikeOutput, error) {
	var out ToggleLikeOutput
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return out, ErrUnauthenticated
	}

	if !reUUID.MatchString(postID) {
		return out, ErrInvalidPostID
	}

	// crdb.ExecuteTx 函数的第4个参数中的操作会被当成事务。将需要执行事务操作的语句都放到这个函数中就可以了。
	err := crdb.ExecuteTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		query := `
			SELECT EXISTS (
				SELECT 1 FROM post_likes WHERE user_id = $1 AND post_id = $2
			)`
		err := tx.QueryRowContext(ctx, query, uid, postID).Scan(&out.Liked)
		if err != nil {
			return fmt.Errorf("could not query select post like existence: %w", err)
		}

		if out.Liked { //取消点赞
			query = "DELETE FROM post_likes WHERE user_id = $1 AND post_id = $2"
			if _, err = tx.ExecContext(ctx, query, uid, postID); err != nil {
				return fmt.Errorf("could not delete post like: %w", err)
			}

			query = "UPDATE posts SET likes_count = likes_count - 1 WHERE id = $1 RETURNING likes_count"
			err = tx.QueryRowContext(ctx, query, postID).Scan(&out.LikesCount)
			if err != nil {
				return fmt.Errorf("could not update and decrement post likes count: %w", err)
			}
		} else {
			query = "INSERT INTO post_likes (user_id, post_id) VALUES ($1, $2)"
			_, err = tx.ExecContext(ctx, query, uid, postID)

			if isForeignKeyViolation(err) {
				return ErrPostNotFound
			}

			if err != nil {
				return fmt.Errorf("could not insert post like: %w", err)
			}

			query = "UPDATE posts SET likes_count = likes_count + 1 WHERE id = $1 RETURNING likes_count"
			err = tx.QueryRowContext(ctx, query, postID).Scan(&out.LikesCount)
			if err != nil {
				return fmt.Errorf("could not update and increment post likes count: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return out, err
	}

	out.Liked = !out.Liked

	return out, nil
}

// TogglePostSubscription so you can stop receiving notifications from a thread.
func (s *Service) TogglePostSubscription(ctx context.Context, postID string) (ToggleSubscriptionOutput, error) {
	var out ToggleSubscriptionOutput
	uid, ok := ctx.Value(KeyAuthUserID).(string)
	if !ok {
		return out, ErrUnauthenticated
	}

	if !reUUID.MatchString(postID) {
		return out, ErrInvalidPostID
	}

	err := crdb.ExecuteTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		query := `SELECT EXISTS (
			SELECT 1 FROM post_subscriptions WHERE user_id = $1 AND post_id = $2
		)`
		err := tx.QueryRowContext(ctx, query, uid, postID).Scan(&out.Subscribed)
		if err != nil {
			return fmt.Errorf("could not query select post subscription existence: %w", err)
		}

		if out.Subscribed {
			query = "DELETE FROM post_subscriptions WHERE user_id = $1 AND post_id = $2"
			if _, err = tx.ExecContext(ctx, query, uid, postID); err != nil {
				return fmt.Errorf("could not delete post subscription: %w", err)
			}
		} else {
			query = "INSERT INTO post_subscriptions (user_id, post_id) VALUES ($1, $2)"
			_, err = tx.ExecContext(ctx, query, uid, postID)
			if isForeignKeyViolation(err) {
				return ErrPostNotFound
			}

			if err != nil {
				return fmt.Errorf("could not insert post subscription: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return out, err
	}

	out.Subscribed = !out.Subscribed

	return out, nil
}
