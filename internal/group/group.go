package group

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type Group struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	AvatarURL      string        `json:"avatar_url"`
	AdminID        string        `json:"admin_id"`
	ConversationID string        `json:"conversation_id"`
	CreatedAt      time.Time     `json:"created_at"`
	Members        []GroupMember `json:"members,omitempty"`
}

type GroupMember struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, adminID, name, description string, memberIDs []string) (*Group, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	groupID := uuid.NewString()
	conversationID := uuid.NewString() // Usually generated to have a unique chat UUID
	
	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups (id, name, description, admin_id, conversation_id)
		VALUES ($1, $2, $3, $4, $5)
	`, groupID, name, description, adminID, conversationID)
	if err != nil {
		return nil, err
	}

	// Add admin to members if not present
	memberIDs = append(memberIDs, adminID)
	
	uniqueMembers := make(map[string]bool)
	for _, id := range memberIDs {
		uniqueMembers[id] = true
	}

	for memberID := range uniqueMembers {
		role := "MEMBER"
		if memberID == adminID {
			role = "ADMIN"
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id, role)
			VALUES ($1, $2, $3)
		`, groupID, memberID, role)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return r.GetByID(ctx, groupID)
}

func (r *Repository) GetByID(ctx context.Context, groupID string) (*Group, error) {
	var g Group
	var convID sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(description, ''), COALESCE(avatar_url, ''), admin_id, conversation_id, created_at
		FROM groups WHERE id = $1
	`, groupID).Scan(&g.ID, &g.Name, &g.Description, &g.AvatarURL, &g.AdminID, &convID, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	if convID.Valid {
		g.ConversationID = convID.String
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT gm.user_id, u.display_name, COALESCE(u.avatar_url, ''), gm.role, gm.joined_at
		FROM group_members gm
		JOIN users u ON gm.user_id = u.id
		WHERE gm.group_id = $1
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		g.Members = append(g.Members, m)
	}

	return &g, nil
}

func (r *Repository) ListUserGroups(ctx context.Context, userID string) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT g.id
		FROM groups g
		JOIN group_members gm ON g.id = gm.group_id
		WHERE gm.user_id = $1
		ORDER BY g.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groupIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, id)
	}
	
	var groups []Group
	for _, id := range groupIDs {
		g, err := r.GetByID(ctx, id)
		if err == nil && g != nil {
			groups = append(groups, *g)
		}
	}

	return groups, nil
}

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, adminID, name, description string, memberIDs []string) (*Group, error) {
	return s.repo.Create(ctx, adminID, name, description, memberIDs)
}

func (s *Service) ListUserGroups(ctx context.Context, userID string) ([]Group, error) {
	return s.repo.ListUserGroups(ctx, userID)
}

// ─── Handler ─────────────────────────────────────────────────────────────────

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

type CreateGroupRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description"`
	MemberIDs   []string `json:"member_ids" binding:"required"`
}

func (h *Handler) Create(c *gin.Context) {
	adminID := c.GetString("user_id")
	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Valid name and member_ids required"})
		return
	}

	group, err := h.service.Create(c.Request.Context(), adminID, req.Name, req.Description, req.MemberIDs)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create group")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create group"})
		return
	}

	c.JSON(http.StatusCreated, group)
}

func (h *Handler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	groups, err := h.service.ListUserGroups(c.Request.Context(), userID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list groups")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list groups"})
		return
	}
	if groups == nil {
		groups = []Group{}
	}
	c.JSON(http.StatusOK, groups)
}
