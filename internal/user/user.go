package user

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatar_url"`
	PublicKey   string `json:"public_key"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// SearchAll returns all users except the requester
func (r *Repository) SearchAll(ctx context.Context, excludeUserID string) ([]User, error) {
	query := `
		SELECT id, display_name, email, COALESCE(avatar_url, ''), COALESCE(public_key, '')
		FROM users
		WHERE id != $1
		ORDER BY display_name ASC
	`
	rows, err := r.db.QueryContext(ctx, query, excludeUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.DisplayName, &u.Email, &u.AvatarURL, &u.PublicKey); err != nil {
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

func (s *Service) SearchAll(ctx context.Context, excludeUserID string) ([]User, error) {
	return s.repo.SearchAll(ctx, excludeUserID)
}

// ─── Handler ─────────────────────────────────────────────────────────────────

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

func (h *Handler) GetMe(c *gin.Context)          {}
func (h *Handler) UpdateProfile(c *gin.Context)  {}
func (h *Handler) UpdateFCMToken(c *gin.Context) {}

// GET /api/v1/users/search
func (h *Handler) Search(c *gin.Context) {
	// Extract userID from JWT middleware
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	users, err := h.service.SearchAll(c.Request.Context(), userID.(string))
	if err != nil {
		log.Error().Err(err).Msg("Failed to search users")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
		return
	}

	// Ensure we never return nil JSON array
	if users == nil {
		users = []User{}
	}

	c.JSON(http.StatusOK, users)
}
