-- ============================================================
-- Chatbook Database Schema
-- PostgreSQL migrations using goose
-- ============================================================

-- +goose Up
-- +goose StatementBegin

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ── Users ────────────────────────────────────────────────────────────────────
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    google_id       VARCHAR(128) UNIQUE NOT NULL,
    email           VARCHAR(320) UNIQUE NOT NULL,
    display_name    VARCHAR(100) NOT NULL,
    avatar_url      TEXT,
    -- X25519 public key (Base64) — client generates, server stores for contact discovery
    -- Private key NEVER leaves the device
    public_key      TEXT,
    status_message  VARCHAR(200) DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_google_id ON users (google_id);
CREATE INDEX idx_users_email ON users (email);

-- ── Devices (for multi-device + FCM tokens) ──────────────────────────────────
CREATE TABLE devices (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fcm_token           TEXT,
    device_fingerprint  VARCHAR(256) NOT NULL,
    platform            VARCHAR(20) DEFAULT 'android',
    last_seen           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, device_fingerprint)
);

CREATE INDEX idx_devices_user_id ON devices (user_id);
CREATE INDEX idx_devices_fcm_token ON devices (fcm_token) WHERE fcm_token IS NOT NULL;

-- ── Refresh Tokens ───────────────────────────────────────────────────────────
CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(64) UNIQUE NOT NULL,  -- SHA-256 of token
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens (user_id);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens (token_hash);

-- ── Contacts ─────────────────────────────────────────────────────────────────
CREATE TABLE contacts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    contact_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    nickname        VARCHAR(100),
    is_blocked      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, contact_id)
);

CREATE INDEX idx_contacts_owner ON contacts (owner_id);
CREATE INDEX idx_contacts_contact ON contacts (contact_id);

-- ── Groups ────────────────────────────────────────────────────────────────────
CREATE TABLE groups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(100) NOT NULL,
    description     VARCHAR(512),
    avatar_url      TEXT,
    admin_id        UUID NOT NULL REFERENCES users(id),
    conversation_id UUID UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE group_members (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        VARCHAR(20) NOT NULL DEFAULT 'MEMBER', -- ADMIN | MEMBER
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(group_id, user_id)
);

CREATE INDEX idx_group_members_group ON group_members (group_id);
CREATE INDEX idx_group_members_user ON group_members (user_id);

-- ── Messages ─────────────────────────────────────────────────────────────────
-- IMPORTANT: Only TEXT messages are stored on the backend.
-- No media (images, videos, documents, files) is ever stored server-side.
-- Audio/video calls are never relayed through the backend.
-- File transfers happen via WebRTC DataChannel (P2P).
CREATE TABLE messages (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id           UUID NOT NULL REFERENCES users(id),
    recipient_id        UUID,           -- NULL for group messages
    group_id            UUID REFERENCES groups(id) ON DELETE SET NULL,
    -- Content is stored encrypted (AES-256-GCM via Signal Double Ratchet)
    -- Backend never sees plaintext
    content_encrypted   TEXT NOT NULL,
    content_iv          TEXT NOT NULL,  -- AES-GCM IV (Base64)
    dh_public_key       TEXT,           -- Signal ratchet DH key for this message
    message_index       INTEGER NOT NULL DEFAULT 0,
    message_type        VARCHAR(30) NOT NULL DEFAULT 'TEXT',
    status              VARCHAR(20) NOT NULL DEFAULT 'SENT',
    sent_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at        TIMESTAMPTZ,
    read_at             TIMESTAMPTZ
);

CREATE INDEX idx_messages_sender ON messages (sender_id);
CREATE INDEX idx_messages_recipient ON messages (recipient_id) WHERE recipient_id IS NOT NULL;
CREATE INDEX idx_messages_group ON messages (group_id) WHERE group_id IS NOT NULL;
CREATE INDEX idx_messages_sent_at ON messages (sent_at DESC);

-- ── Offline Message Queue ─────────────────────────────────────────────────────
-- When recipient is offline, messages are queued for delivery
CREATE TABLE offline_message_queue (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id  UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    queued_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(message_id, user_id)
);

CREATE INDEX idx_offline_queue_user ON offline_message_queue (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS offline_message_queue;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
