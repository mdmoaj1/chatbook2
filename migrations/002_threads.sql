-- ============================================================
-- Chatbook Database Schema — Threads / Social Feed
-- Migration 002
-- ============================================================

-- +goose Up
-- +goose StatementBegin

-- ── Threads (text-only posts, no images) ──────────────────────────────────────
CREATE TABLE IF NOT EXISTS threads (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content     TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 1000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_threads_author    ON threads (author_id);
CREATE INDEX IF NOT EXISTS idx_threads_created   ON threads (created_at DESC);

-- ── Thread Reactions (6 Facebook-style emoji) ─────────────────────────────────
-- Allowed emojis: 👍 ❤️ 😂 😮 😢 😡
CREATE TABLE IF NOT EXISTS thread_reactions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id   UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji       VARCHAR(8) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(thread_id, user_id, emoji)
);

CREATE INDEX IF NOT EXISTS idx_reactions_thread ON thread_reactions (thread_id);

-- ── Thread Comments ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS thread_comments (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id   UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content     TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 500),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_comments_thread  ON thread_comments (thread_id);
CREATE INDEX IF NOT EXISTS idx_comments_author  ON thread_comments (author_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS thread_comments;
DROP TABLE IF EXISTS thread_reactions;
DROP TABLE IF EXISTS threads;
-- +goose StatementEnd
