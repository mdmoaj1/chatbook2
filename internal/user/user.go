package user

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type User struct {
	ID            string    `json:"id"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email"`
	AvatarURL     string    `json:"avatar_url"`
	PublicKey     string    `json:"public_key"`
	StatusMessage string    `json:"status_message"`
	CreatedAt     time.Time `json:"created_at"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetByID(ctx context.Context, userID string) (*User, error) {
	u := &User{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, display_name, email,
		       COALESCE(avatar_url, ''), COALESCE(public_key, ''),
		       COALESCE(status_message, ''), created_at
		FROM users WHERE id = $1
	`, userID).Scan(&u.ID, &u.DisplayName, &u.Email, &u.AvatarURL,
		&u.PublicKey, &u.StatusMessage, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (r *Repository) UpdateProfile(ctx context.Context, userID, displayName, statusMessage, publicKey string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE users
		SET display_name   = COALESCE(NULLIF($2, ''), display_name),
		    status_message = $3,
		    public_key     = COALESCE(NULLIF($4, ''), public_key),
		    updated_at     = NOW()
		WHERE id = $1
	`, userID, displayName, statusMessage, publicKey)
	return err
}

func (r *Repository) UpsertFCMToken(ctx context.Context, userID, fcmToken, deviceFingerprint string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO devices (user_id, fcm_token, device_fingerprint, last_seen)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, device_fingerprint)
		DO UPDATE SET fcm_token = $2, last_seen = NOW()
	`, userID, fcmToken, deviceFingerprint)
	return err
}

func (r *Repository) SearchAll(ctx context.Context, query, excludeUserID string) ([]User, error) {
	var rows *sql.Rows
	var err error

	if query == "" {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, display_name, email,
			       COALESCE(avatar_url, ''), COALESCE(public_key, ''),
			       COALESCE(status_message, ''), created_at
			FROM users
			WHERE id != $1
			ORDER BY display_name ASC
			LIMIT 200
		`, excludeUserID)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id, display_name, email,
			       COALESCE(avatar_url, ''), COALESCE(public_key, ''),
			       COALESCE(status_message, ''), created_at
			FROM users
			WHERE id != $1
			  AND (LOWER(display_name) LIKE '%' || LOWER($2) || '%'
			    OR LOWER(email)        LIKE '%' || LOWER($2) || '%')
			ORDER BY display_name ASC
			LIMIT 50
		`, excludeUserID, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.Email, &u.AvatarURL,
			&u.PublicKey, &u.StatusMessage, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetMe(ctx context.Context, userID string) (*User, error) {
	return s.repo.GetByID(ctx, userID)
}

func (s *Service) UpdateProfile(ctx context.Context, userID, displayName, statusMessage, publicKey string) (*User, error) {
	if err := s.repo.UpdateProfile(ctx, userID, displayName, statusMessage, publicKey); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, userID)
}

func (s *Service) UpsertFCMToken(ctx context.Context, userID, fcmToken, deviceFingerprint string) error {
	return s.repo.UpsertFCMToken(ctx, userID, fcmToken, deviceFingerprint)
}

func (s *Service) SearchAll(ctx context.Context, query, excludeUserID string) ([]User, error) {
	return s.repo.SearchAll(ctx, query, excludeUserID)
}

// ─── Handler ─────────────────────────────────────────────────────────────────

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// GET /api/v1/users/me
func (h *Handler) GetMe(c *gin.Context) {
	userID := c.GetString("user_id")
	user, err := h.service.GetMe(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get user profile")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// PUT /api/v1/users/me
type UpdateProfileRequest struct {
	DisplayName   string `json:"display_name"`
	StatusMessage string `json:"status_message"`
	PublicKey     string `json:"public_key"`
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID := c.GetString("user_id")
	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	user, err := h.service.UpdateProfile(c.Request.Context(), userID, req.DisplayName, req.StatusMessage, req.PublicKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update profile")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update profile"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// POST /api/v1/users/me/fcm-token
type UpdateFCMTokenRequest struct {
	FCMToken          string `json:"fcm_token" binding:"required"`
	DeviceFingerprint string `json:"device_fingerprint" binding:"required"`
}

func (h *Handler) UpdateFCMToken(c *gin.Context) {
	userID := c.GetString("user_id")
	var req UpdateFCMTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fcm_token and device_fingerprint required"})
		return
	}
	if err := h.service.UpsertFCMToken(c.Request.Context(), userID, req.FCMToken, req.DeviceFingerprint); err != nil {
		log.Error().Err(err).Msg("Failed to update FCM token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update FCM token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// GET /api/v1/users/search?q=optional_query
func (h *Handler) Search(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	query := c.Query("q")
	users, err := h.service.SearchAll(c.Request.Context(), query, userID.(string))
	if err != nil {
		log.Error().Err(err).Msg("Failed to search users")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
		return
	}
	if users == nil {
		users = []User{}
	}
	c.JSON(http.StatusOK, users)
}
