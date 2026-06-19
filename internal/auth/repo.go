package auth

import (
	"context"
	"database/sql"
	"time"
)

type User struct {
	ID          string
	GoogleID    string
	Email       string
	DisplayName string
	AvatarURL   string
	PublicKey   string
	CreatedAt   time.Time
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) FindByGoogleID(ctx context.Context, googleID string) (*User, error) {
	u := &User{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, google_id, email, display_name,
		       COALESCE(avatar_url, ''), COALESCE(public_key, ''), created_at
		FROM users WHERE google_id = $1
	`, googleID).Scan(
		&u.ID, &u.GoogleID, &u.Email, &u.DisplayName,
		&u.AvatarURL, &u.PublicKey, &u.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *Repository) Create(ctx context.Context, user *User) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO users (id, google_id, email, display_name, avatar_url, public_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		RETURNING id
	`, user.ID, user.GoogleID, user.Email, user.DisplayName,
		user.AvatarURL, user.PublicKey, user.CreatedAt,
	).Scan(&user.ID)
}

func (r *Repository) Update(ctx context.Context, user *User) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users SET display_name = $1, avatar_url = $2, updated_at = NOW()
		WHERE id = $3
	`, user.DisplayName, user.AvatarURL, user.ID)
	return err
}

// ─── Refresh Tokens ───────────────────────────────────────────────────────────

type RefreshToken struct {
	UserID    string
	Revoked   bool
	ExpiresAt time.Time
}

func (r *Repository) StoreRefreshToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (token_hash) DO NOTHING
	`, userID, tokenHash, expiresAt)
	return err
}

func (r *Repository) FindRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	rt := &RefreshToken{}
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, revoked, expires_at FROM refresh_tokens WHERE token_hash = $1
	`, tokenHash).Scan(&rt.UserID, &rt.Revoked, &rt.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rt, nil
}

func (r *Repository) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash)
	return err
}

func (r *Repository) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, userID)
	return err
}
