package contact

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/chatbook/backend/internal/presence"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

type Contact struct {
	ID            string    `json:"id"`
	ContactUserID string    `json:"contact_user_id"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email"`
	AvatarURL     string    `json:"avatar_url"`
	PublicKey     string    `json:"public_key"`
	IsBlocked     bool      `json:"is_blocked"`
	IsOnline      bool      `json:"is_online"`
	CreatedAt     time.Time `json:"created_at"`
}

type FriendRequest struct {
	ID          string    `json:"id"`
	SenderID    string    `json:"sender_id"`
	SenderName  string    `json:"sender_name"`
	SenderEmail string    `json:"sender_email"`
	SenderAvatar string   `json:"sender_avatar"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) List(ctx context.Context, ownerID string) ([]Contact, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.contact_id,
		       u.display_name, u.email,
		       COALESCE(u.avatar_url, ''), COALESCE(u.public_key, ''),
		       c.is_blocked, c.created_at
		FROM contacts c
		JOIN users u ON u.id = c.contact_id
		WHERE c.owner_id = $1
		ORDER BY u.display_name ASC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.ContactUserID, &c.DisplayName, &c.Email,
			&c.AvatarURL, &c.PublicKey, &c.IsBlocked, &c.CreatedAt); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, rows.Err()
}

func (r *Repository) Add(ctx context.Context, ownerID, email string) (*Contact, error) {
	// Find target user by email
	var targetID, displayName, avatarURL, publicKey string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, display_name, COALESCE(avatar_url, ''), COALESCE(public_key, '')
		FROM users WHERE email = $1
	`, email).Scan(&targetID, &displayName, &avatarURL, &publicKey)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if targetID == ownerID {
		return nil, nil
	}

	var contactID string
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO contacts (owner_id, contact_id)
		VALUES ($1, $2)
		ON CONFLICT (owner_id, contact_id) DO UPDATE SET is_blocked = FALSE
		RETURNING id
	`, ownerID, targetID).Scan(&contactID)
	if err != nil {
		return nil, err
	}

	return &Contact{
		ID:            contactID,
		ContactUserID: targetID,
		DisplayName:   displayName,
		Email:         email,
		AvatarURL:     avatarURL,
		PublicKey:     publicKey,
	}, nil
}

func (r *Repository) Remove(ctx context.Context, ownerID, contactID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM contacts WHERE id = $1 AND owner_id = $2`, contactID, ownerID)
	return err
}

func (r *Repository) SetBlocked(ctx context.Context, ownerID, contactID string, blocked bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE contacts SET is_blocked = $3 WHERE id = $1 AND owner_id = $2`,
		contactID, ownerID, blocked)
	return err
}

// ─── Friend Requests ─────────────────────────────────────────────────────────

func (r *Repository) SendFriendRequest(ctx context.Context, senderID, receiverEmail string) (*FriendRequest, error) {
	// Find receiver by email
	var receiverID string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, receiverEmail).Scan(&receiverID)
	if err == sql.ErrNoRows {
		return nil, nil // User not found
	}
	if err != nil {
		return nil, err
	}
	if receiverID == senderID {
		return nil, nil // Cannot send to self
	}

	// Check if already friends
	var count int
	r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE owner_id = $1 AND contact_id = $2`, senderID, receiverID).Scan(&count)
	if count > 0 {
		return nil, nil // Already friends
	}

	id := uuid.NewString()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO friend_requests (id, sender_id, receiver_id, status)
		VALUES ($1, $2, $3, 'pending')
		ON CONFLICT (sender_id, receiver_id) DO NOTHING
	`, id, senderID, receiverID)
	if err != nil {
		return nil, err
	}

	return &FriendRequest{
		ID:       id,
		SenderID: senderID,
		Status:   "pending",
	}, nil
}

func (r *Repository) ListPendingRequests(ctx context.Context, receiverID string) ([]FriendRequest, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.sender_id, u.display_name, u.email, COALESCE(u.avatar_url, ''), f.status, f.created_at
		FROM friend_requests f
		JOIN users u ON u.id = f.sender_id
		WHERE f.receiver_id = $1 AND f.status = 'pending'
		ORDER BY f.created_at DESC
	`, receiverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []FriendRequest
	for rows.Next() {
		var req FriendRequest
		if err := rows.Scan(&req.ID, &req.SenderID, &req.SenderName, &req.SenderEmail, &req.SenderAvatar, &req.Status, &req.CreatedAt); err != nil {
			return nil, err
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

func (r *Repository) AcceptFriendRequest(ctx context.Context, receiverID, requestID string) error {
	var senderID string
	err := r.db.QueryRowContext(ctx, `
		UPDATE friend_requests SET status = 'accepted', updated_at = NOW() 
		WHERE id = $1 AND receiver_id = $2 AND status = 'pending' 
		RETURNING sender_id
	`, requestID, receiverID).Scan(&senderID)
	if err == sql.ErrNoRows {
		return nil // Not found or already handled
	}
	if err != nil {
		return err
	}

	// Insert bi-directional contacts
	c1 := uuid.NewString()
	c2 := uuid.NewString()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO contacts (id, owner_id, contact_id) VALUES ($1, $2, $3)
		ON CONFLICT (owner_id, contact_id) DO NOTHING
	`, c1, receiverID, senderID)
	
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO contacts (id, owner_id, contact_id) VALUES ($1, $2, $3)
		ON CONFLICT (owner_id, contact_id) DO NOTHING
	`, c2, senderID, receiverID)

	return err
}

func (r *Repository) RejectFriendRequest(ctx context.Context, receiverID, requestID string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE friend_requests SET status = 'rejected', updated_at = NOW() 
		WHERE id = $1 AND receiver_id = $2 AND status = 'pending'
	`, requestID, receiverID)
	return err
}

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	repo            *Repository
	presenceService *presence.Service
}

func NewService(repo *Repository, presenceService *presence.Service) *Service {
	return &Service{repo: repo, presenceService: presenceService}
}

func (s *Service) List(ctx context.Context, ownerID string) ([]Contact, error) {
	contacts, err := s.repo.List(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	// Enrich with online status
	userIDs := make([]string, len(contacts))
	for i, c := range contacts {
		userIDs[i] = c.ContactUserID
	}
	online := s.presenceService.GetOnlineUsers(userIDs)
	onlineSet := make(map[string]bool, len(online))
	for _, uid := range online {
		onlineSet[uid] = true
	}
	for i := range contacts {
		contacts[i].IsOnline = onlineSet[contacts[i].ContactUserID]
	}
	return contacts, nil
}

func (s *Service) Add(ctx context.Context, ownerID, email string) (*Contact, error) {
	return s.repo.Add(ctx, ownerID, email)
}

func (s *Service) Remove(ctx context.Context, ownerID, contactID string) error {
	return s.repo.Remove(ctx, ownerID, contactID)
}

func (s *Service) SetBlocked(ctx context.Context, ownerID, contactID string, blocked bool) error {
	return s.repo.SetBlocked(ctx, ownerID, contactID, blocked)
}

func (s *Service) SendFriendRequest(ctx context.Context, senderID, receiverEmail string) (*FriendRequest, error) {
	return s.repo.SendFriendRequest(ctx, senderID, receiverEmail)
}

func (s *Service) ListPendingRequests(ctx context.Context, receiverID string) ([]FriendRequest, error) {
	return s.repo.ListPendingRequests(ctx, receiverID)
}

func (s *Service) AcceptFriendRequest(ctx context.Context, receiverID, requestID string) error {
	return s.repo.AcceptFriendRequest(ctx, receiverID, requestID)
}

func (s *Service) RejectFriendRequest(ctx context.Context, receiverID, requestID string) error {
	return s.repo.RejectFriendRequest(ctx, receiverID, requestID)
}

// ─── Handler ─────────────────────────────────────────────────────────────────

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// GET /api/v1/contacts
func (h *Handler) List(c *gin.Context) {
	ownerID := c.GetString("user_id")
	contacts, err := h.service.List(c.Request.Context(), ownerID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list contacts")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list contacts"})
		return
	}
	if contacts == nil {
		contacts = []Contact{}
	}
	c.JSON(http.StatusOK, contacts)
}

// POST /api/v1/contacts
type AddContactRequest struct {
	Email string `json:"email" binding:"required,email"`
}

func (h *Handler) Add(c *gin.Context) {
	ownerID := c.GetString("user_id")
	var req AddContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Valid email required"})
		return
	}
	contact, err := h.service.Add(c.Request.Context(), ownerID, req.Email)
	if err != nil {
		log.Error().Err(err).Msg("Failed to add contact")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add contact"})
		return
	}
	if contact == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found or cannot add yourself"})
		return
	}
	c.JSON(http.StatusCreated, contact)
}

// DELETE /api/v1/contacts/:id
func (h *Handler) Remove(c *gin.Context) {
	ownerID := c.GetString("user_id")
	contactID := c.Param("id")
	if err := h.service.Remove(c.Request.Context(), ownerID, contactID); err != nil {
		log.Error().Err(err).Msg("Failed to remove contact")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove contact"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

// POST /api/v1/contacts/:id/block
func (h *Handler) Block(c *gin.Context) {
	ownerID := c.GetString("user_id")
	contactID := c.Param("id")
	if err := h.service.SetBlocked(c.Request.Context(), ownerID, contactID, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to block contact"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "blocked"})
}

// POST /api/v1/contacts/:id/unblock
func (h *Handler) Unblock(c *gin.Context) {
	ownerID := c.GetString("user_id")
	contactID := c.Param("id")
	if err := h.service.SetBlocked(c.Request.Context(), ownerID, contactID, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to unblock contact"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unblocked"})
}

// ─── Friend Requests Handlers ────────────────────────────────────────────────

// POST /api/v1/contacts/requests
type SendRequestReq struct {
	Email string `json:"email" binding:"required,email"`
}

func (h *Handler) SendRequest(c *gin.Context) {
	senderID := c.GetString("user_id")
	var req SendRequestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Valid email required"})
		return
	}
	fr, err := h.service.SendFriendRequest(c.Request.Context(), senderID, req.Email)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send friend request")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send request"})
		return
	}
	if fr == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found, already friends, or invalid"})
		return
	}
	c.JSON(http.StatusCreated, fr)
}

// GET /api/v1/contacts/requests
func (h *Handler) ListRequests(c *gin.Context) {
	receiverID := c.GetString("user_id")
	reqs, err := h.service.ListPendingRequests(c.Request.Context(), receiverID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list friend requests")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list requests"})
		return
	}
	if reqs == nil {
		reqs = []FriendRequest{}
	}
	c.JSON(http.StatusOK, reqs)
}

// POST /api/v1/contacts/requests/:id/accept
func (h *Handler) AcceptRequest(c *gin.Context) {
	receiverID := c.GetString("user_id")
	reqID := c.Param("id")
	if err := h.service.AcceptFriendRequest(c.Request.Context(), receiverID, reqID); err != nil {
		log.Error().Err(err).Msg("Failed to accept friend request")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to accept request"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "accepted"})
}

// POST /api/v1/contacts/requests/:id/reject
func (h *Handler) RejectRequest(c *gin.Context) {
	receiverID := c.GetString("user_id")
	reqID := c.Param("id")
	if err := h.service.RejectFriendRequest(c.Request.Context(), receiverID, reqID); err != nil {
		log.Error().Err(err).Msg("Failed to reject friend request")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reject request"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}
