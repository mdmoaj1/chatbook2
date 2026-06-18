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
	return nil, nil
}

func (r *Repository) Update(ctx context.Context, user *User) error {
	return nil
}

func (r *Repository) Create(ctx context.Context, user *User) error {
	return nil
}

type RefreshToken struct {
	UserID    string
	Revoked   bool
	ExpiresAt time.Time
}

func (r *Repository) StoreRefreshToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	return nil
}

func (r *Repository) FindRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	return nil, nil
}

func (r *Repository) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	return nil
}

func (r *Repository) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	return nil
}
