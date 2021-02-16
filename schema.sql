-- CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

DROP DATABASE IF EXISTS nakama CASCADE;
CREATE DATABASE IF NOT EXISTS nakama;
SET DATABASE = nakama;

CREATE TABLE IF NOT EXISTS users (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR NOT NULL UNIQUE,
    username VARCHAR NOT NULL UNIQUE,
    avatar VARCHAR, -- 头像
    followers_count INT NOT NULL DEFAULT 0 CHECK (followers_count >= 0), -- 关注我的用户数量
    followees_count INT NOT NULL DEFAULT 0 CHECK (followees_count >= 0) -- 我关注的用户数量
);

CREATE TABLE IF NOT EXISTS verification_codes (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL REFERENCES users, --发起关注的人的ID
    followee_id UUID NOT NULL REFERENCES users, --被关注的人的ID
    PRIMARY KEY (follower_id, followee_id) -- 联合主键
);

-- 记录一次发帖的表
CREATE TABLE IF NOT EXISTS posts (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(), -- 帖子的ID
    user_id UUID NOT NULL REFERENCES users, -- 发帖的用户的ID
    content VARCHAR NOT NULL,  -- 帖子内容
    spoiler_of VARCHAR, -- 设置的是黑名单吗？？
    nsfw BOOLEAN NOT NULL DEFAULT false, -- not-safe-for-work（不安全的工作方式），这用来标记这个帖子是否有不安全的信息，用来进行警告用户
    likes_count INT NOT NULL DEFAULT 0 CHECK (likes_count >= 0), -- 帖子点赞数量
    comments_count INT NOT NULL DEFAULT 0 CHECK (comments_count >= 0), --评论数量
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() --发帖时间
);

CREATE INDEX IF NOT EXISTS sorted_posts ON posts (created_at DESC);

-- 帖子点赞的表
CREATE TABLE IF NOT EXISTS post_likes (
    user_id UUID NOT NULL REFERENCES users,
    post_id UUID NOT NULL REFERENCES posts,
    PRIMARY KEY (user_id, post_id)
);

-- 用于用户订阅某个帖子的表？？当这个帖子更新时，通知订阅了这个帖子的用户
CREATE TABLE IF NOT EXISTS post_subscriptions (
    user_id UUID NOT NULL REFERENCES users,
    post_id UUID NOT NULL REFERENCES posts,
    PRIMARY KEY (user_id, post_id)
);

-- 时间线表，目前还不清楚作用
CREATE TABLE IF NOT EXISTS timeline (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users,
    post_id UUID NOT NULL REFERENCES posts
);

CREATE UNIQUE INDEX IF NOT EXISTS unique_timeline_items ON timeline (user_id, post_id);

-- 评论表
CREATE TABLE IF NOT EXISTS comments (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(), -- 评论的id
    user_id UUID NOT NULL REFERENCES users, -- 评论的用户id
    post_id UUID NOT NULL REFERENCES posts,  -- 评论的帖子
    content VARCHAR NOT NULL, --评论的内容
    likes_count INT NOT NULL DEFAULT 0 CHECK (likes_count >= 0), -- 评论的点赞数量
    created_at TIMESTAMPTZ NOT NULL DEFAULT now() -- 评论发布时间
);

CREATE INDEX IF NOT EXISTS sorted_comments ON comments (created_at DESC);

-- 评论点赞的表
CREATE TABLE IF NOT EXISTS comment_likes (
    user_id UUID NOT NULL REFERENCES users,
    comment_id UUID NOT NULL REFERENCES comments,
    PRIMARY KEY (user_id, comment_id)
);

-- 通知表，当有新的关注，帖子有了评论，评论有了回复, 被别人@  这几种情况都应该进行通知
CREATE TABLE IF NOT EXISTS notifications (
    id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users,
    actors VARCHAR[] NOT NULL,
    type VARCHAR NOT NULL,
    post_id UUID REFERENCES posts,
    read_at TIMESTAMPTZ,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sorted_notifications ON notifications (issued_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS unique_notifications ON notifications (user_id, type, post_id, read_at);

-- 下面是插入一些用于测试的数据
INSERT INTO users (id, email, username) VALUES
    ('24ca6ce6-b3e9-4276-a99a-45c77115cc9f', 'shinji@example.org', 'shinji'),
    ('93dfcef9-0b45-46ae-933c-ea52fbf80edb', 'rei@example.org', 'rei');

INSERT INTO posts (id, user_id, content, comments_count) VALUES
    ('c592451b-fdd2-430d-8d49-e75f058c3dce', '24ca6ce6-b3e9-4276-a99a-45c77115cc9f', 'sample post', 1);
INSERT INTO post_subscriptions (user_id, post_id) VALUES
    ('24ca6ce6-b3e9-4276-a99a-45c77115cc9f', 'c592451b-fdd2-430d-8d49-e75f058c3dce');
INSERT INTO timeline (id, user_id, post_id) VALUES
    ('d7490258-1f2f-4a75-8fbb-1846ccde9543', '24ca6ce6-b3e9-4276-a99a-45c77115cc9f', 'c592451b-fdd2-430d-8d49-e75f058c3dce');

INSERT INTO comments (id, user_id, post_id, content) VALUES
    ('648e60bf-b0ab-42e6-8e48-10f797b19c49', '24ca6ce6-b3e9-4276-a99a-45c77115cc9f', 'c592451b-fdd2-430d-8d49-e75f058c3dce', 'sample comment');
