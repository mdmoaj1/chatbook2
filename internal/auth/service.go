package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/chatbook/backend/pkg/googleauth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Service struct {
	repo           *Repository
	jwtSecret      []byte
	googleClientID string
}

func NewService(repo *Repository, jwtSecret, googleClientID string) *Service {
	return &Service{
		repo:           repo,
		jwtSecret:      []byte(jwtSecret),
		googleClientID: googleClientID,
	}
}

// ─── Google Token Verification ────────────────────────────────────────────────

type GoogleUserInfo struct {
	GoogleID    string
	Email       string
	DisplayName string
	AvatarURL   string
}

func (s *Service) VerifyGoogleToken(ctx context.Context, idToken string) (*GoogleUserInfo, error) {
	payload, err := googleauth.VerifyIDToken(ctx, idToken, s.googleClientID)
	if err != nil {
		return nil, fmt.Errorf("invalid google token: %w", err)
	}
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	picture, _ := payload.Claims["picture"].(string)

	return &GoogleUserInfo{
		GoogleID:    payload.Subject,
		Email:       email,
		DisplayName: name,
		AvatarURL:   picture,
	}, nil
}

// ─── Upsert User ─────────────────────────────────────────────────────────────

func (s *Service) UpsertUser(ctx context.Context, info *GoogleUserInfo) (*User, error) {
	existing, err := s.repo.FindByGoogleID(ctx, info.GoogleID)
	if err == nil && existing != nil {
		// Update display name and avatar on each login
		existing.DisplayName = info.DisplayName
		existing.AvatarURL   = info.AvatarURL
		if err := s.repo.Update(ctx, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}

	// Create new user
	newUser := &User{
		ID:          uuid.NewString(),
		GoogleID:    info.GoogleID,
		Email:       info.Email,
		DisplayName: info.DisplayName,
		AvatarURL:   info.AvatarURL,
		PublicKey:   "", // Client will update after first login
		CreatedAt:   time.Now(),
	}
	if err := s.repo.Create(ctx, newUser); err != nil {
		return nil, err
	}
	return newUser, nil
}

// ─── JWT Generation ───────────────────────────────────────────────────────────

type Claims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

func (s *Service) GenerateTokens(userID string) (accessToken, refreshToken string, err error) {
	// Access token (15 minutes)
	accessClaims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   userID,
		},
	}
	accessToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.jwtSecret)
	if err != nil {
		return "", "", err
	}

	// Refresh token (30 days) — opaque random token
	refreshToken = uuid.NewString() + "-" + uuid.NewString()
	return accessToken, refreshToken, nil
}

func (s *Service) ValidateAccessToken(tokenStr string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}
	return claims.UserID, nil
}

// ─── Refresh Token Management ─────────────────────────────────────────────────

func (s *Service) StoreRefreshToken(ctx context.Context, userID, token string) error {
	hash := hashToken(token)
	return s.repo.StoreRefreshToken(ctx, userID, hash, time.Now().Add(30*24*time.Hour))
}

func (s *Service) RefreshTokens(ctx context.Context, refreshToken string) (userID, newAccess, newRefresh string, err error) {
	hash := hashToken(refreshToken)
	storedToken, err := s.repo.FindRefreshToken(ctx, hash)
	if err != nil || storedToken == nil || storedToken.Revoked {
		return "", "", "", fmt.Errorf("invalid refresh token")
	}
	if time.Now().After(storedToken.ExpiresAt) {
		return "", "", "", fmt.Errorf("refresh token expired")
	}

	// Rotate — revoke old, issue new
	if err := s.repo.RevokeRefreshToken(ctx, hash); err != nil {
		return "", "", "", err
	}

	newAccess, newRefresh, err = s.GenerateTokens(storedToken.UserID)
	if err != nil {
		return "", "", "", err
	}
	if err := s.StoreRefreshToken(ctx, storedToken.UserID, newRefresh); err != nil {
		return "", "", "", err
	}

	return storedToken.UserID, newAccess, newRefresh, nil
}

func (s *Service) RevokeTokens(ctx context.Context, userID, accessToken string) error {
	return s.repo.RevokeAllUserRefreshTokens(ctx, userID)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
