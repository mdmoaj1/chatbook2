package thread

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// ─── Models ──────────────────────────────────────────────────────────────────

var AllowedEmoji = map[string]bool{
	"👍": true, "❤️": true, "😂": true,
	"😮": true, "😢": true, "😡": true,
}

type Thread struct {
	ID            string             `json:"id"`
	AuthorID      string             `json:"author_id"`
	AuthorName    string             `json:"author_name"`
	AuthorAvatar  string             `json:"author_avatar"`
	Content       string             `json:"content"`
	CreatedAt     time.Time          `json:"created_at"`
	Reactions     []ReactionSummary  `json:"reactions"`
	CommentCount  int                `json:"comment_count"`
	UserReaction  string             `json:"user_reaction"` // Current user's reaction, or ""
}

type ReactionSummary struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
}

type Comment struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"thread_id"`
	AuthorID     string    `json:"author_id"`
	AuthorName   string    `json:"author_name"`
	AuthorAvatar string    `json:"author_avatar"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"created_at"`
}

// ─── Repository ──────────────────────────────────────────────────────────────

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateThread(ctx context.Context, authorID, content string) (*Thread, error) {
	id := uuid.NewString()
	now := time.Now()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO threads (id, author_id, content, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
	`, id, authorID, content, now)
	if err != nil {
		return nil, err
	}
	return &Thread{ID: id, AuthorID: authorID, Content: content, CreatedAt: now, Reactions: []ReactionSummary{}}, nil
}

func (r *Repository) ListFeed(ctx context.Context, requestingUserID string, limit int, before time.Time) ([]Thread, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.author_id,
		       u.display_name, COALESCE(u.avatar_url,''),
		       t.content, t.created_at,
		       (SELECT COUNT(*) FROM thread_comments c WHERE c.thread_id = t.id),
		       COALESCE((SELECT emoji FROM thread_reactions WHERE thread_id = t.id AND user_id = $2 LIMIT 1), '')
		FROM threads t
		JOIN users u ON u.id = t.author_id
		WHERE t.created_at < $3
		ORDER BY t.created_at DESC
		LIMIT $1
	`, limit, requestingUserID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ID, &t.AuthorID, &t.AuthorName, &t.AuthorAvatar,
			&t.Content, &t.CreatedAt, &t.CommentCount, &t.UserReaction); err != nil {
			return nil, err
		}
		threads = append(threads, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Bulk-load reactions for all threads
	if len(threads) > 0 {
		ids := make([]string, len(threads))
		for i, t := range threads {
			ids[i] = "'" + t.ID + "'"
		}
		rrows, err := r.db.QueryContext(ctx,
			`SELECT thread_id, emoji, COUNT(*) FROM thread_reactions
			 WHERE thread_id = ANY(ARRAY[`+strings.Join(ids, ",")+`]::UUID[])
			 GROUP BY thread_id, emoji`)
		if err == nil {
			defer rrows.Close()
			reactionMap := make(map[string][]ReactionSummary)
			for rrows.Next() {
				var tid, emoji string
				var count int
				rrows.Scan(&tid, &emoji, &count)
				reactionMap[tid] = append(reactionMap[tid], ReactionSummary{Emoji: emoji, Count: count})
			}
			for i := range threads {
				if rs, ok := reactionMap[threads[i].ID]; ok {
					threads[i].Reactions = rs
				} else {
					threads[i].Reactions = []ReactionSummary{}
				}
			}
		}
	}

	return threads, nil
}

func (r *Repository) GetThread(ctx context.Context, threadID, requestingUserID string) (*Thread, error) {
	t := &Thread{}
	err := r.db.QueryRowContext(ctx, `
		SELECT t.id, t.author_id,
		       u.display_name, COALESCE(u.avatar_url,''),
		       t.content, t.created_at,
		       (SELECT COUNT(*) FROM thread_comments c WHERE c.thread_id = t.id),
		       COALESCE((SELECT emoji FROM thread_reactions WHERE thread_id = t.id AND user_id = $2 LIMIT 1), '')
		FROM threads t JOIN users u ON u.id = t.author_id
		WHERE t.id = $1
	`, threadID, requestingUserID).Scan(
		&t.ID, &t.AuthorID, &t.AuthorName, &t.AuthorAvatar,
		&t.Content, &t.CreatedAt, &t.CommentCount, &t.UserReaction)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Load reactions
	rrows, err := r.db.QueryContext(ctx,
		`SELECT emoji, COUNT(*) FROM thread_reactions WHERE thread_id = $1 GROUP BY emoji`, threadID)
	if err == nil {
		defer rrows.Close()
		for rrows.Next() {
			var rs ReactionSummary
			rrows.Scan(&rs.Emoji, &rs.Count)
			t.Reactions = append(t.Reactions, rs)
		}
	}
	if t.Reactions == nil {
		t.Reactions = []ReactionSummary{}
	}
	return t, nil
}

func (r *Repository) ToggleReaction(ctx context.Context, threadID, userID, emoji string) error {
	// Check if already reacted with this emoji
	var count int
	r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM thread_reactions WHERE thread_id=$1 AND user_id=$2 AND emoji=$3`,
		threadID, userID, emoji).Scan(&count)

	if count > 0 {
		// Remove reaction (toggle off)
		_, err := r.db.ExecContext(ctx,
			`DELETE FROM thread_reactions WHERE thread_id=$1 AND user_id=$2 AND emoji=$3`,
			threadID, userID, emoji)
		return err
	}
	// Remove any existing different reaction first (only one emoji per user per thread)
	r.db.ExecContext(ctx,
		`DELETE FROM thread_reactions WHERE thread_id=$1 AND user_id=$2`,
		threadID, userID)

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO thread_reactions (thread_id, user_id, emoji) VALUES ($1, $2, $3)`,
		threadID, userID, emoji)
	return err
}

func (r *Repository) AddComment(ctx context.Context, threadID, authorID, content string) (*Comment, error) {
	id := uuid.NewString()
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO thread_comments (id, thread_id, author_id, content, created_at) VALUES ($1,$2,$3,$4,$5)`,
		id, threadID, authorID, content, now)
	if err != nil {
		return nil, err
	}
	// Get author info
	var name, avatar string
	r.db.QueryRowContext(ctx, `SELECT display_name, COALESCE(avatar_url,'') FROM users WHERE id=$1`, authorID).
		Scan(&name, &avatar)

	return &Comment{ID: id, ThreadID: threadID, AuthorID: authorID, AuthorName: name, AuthorAvatar: avatar, Content: content, CreatedAt: now}, nil
}

func (r *Repository) GetComments(ctx context.Context, threadID string, limit int) ([]Comment, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.thread_id, c.author_id,
		       u.display_name, COALESCE(u.avatar_url,''),
		       c.content, c.created_at
		FROM thread_comments c
		JOIN users u ON u.id = c.author_id
		WHERE c.thread_id = $1
		ORDER BY c.created_at ASC
		LIMIT $2
	`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.ThreadID, &c.AuthorID, &c.AuthorName, &c.AuthorAvatar, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	if comments == nil {
		comments = []Comment{}
	}
	return comments, rows.Err()
}

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateThread(ctx context.Context, authorID, content string) (*Thread, error) {
	return s.repo.CreateThread(ctx, authorID, content)
}

func (s *Service) ListFeed(ctx context.Context, userID string, limit int, before time.Time) ([]Thread, error) {
	return s.repo.ListFeed(ctx, userID, limit, before)
}

func (s *Service) GetThread(ctx context.Context, threadID, userID string) (*Thread, error) {
	return s.repo.GetThread(ctx, threadID, userID)
}

func (s *Service) ToggleReaction(ctx context.Context, threadID, userID, emoji string) error {
	if !AllowedEmoji[emoji] {
		return nil
	}
	return s.repo.ToggleReaction(ctx, threadID, userID, emoji)
}

func (s *Service) AddComment(ctx context.Context, threadID, authorID, content string) (*Comment, error) {
	return s.repo.AddComment(ctx, threadID, authorID, content)
}

func (s *Service) GetComments(ctx context.Context, threadID string, limit int) ([]Comment, error) {
	return s.repo.GetComments(ctx, threadID, limit)
}

// ─── Handler ─────────────────────────────────────────────────────────────────

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// POST /api/v1/threads
type CreateThreadRequest struct {
	Content string `json:"content" binding:"required,min=1,max=1000"`
}

func (h *Handler) CreateThread(c *gin.Context) {
	userID := c.GetString("user_id")
	var req CreateThreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content required (1-1000 chars)"})
		return
	}
	thread, err := h.service.CreateThread(c.Request.Context(), userID, req.Content)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create thread")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create post"})
		return
	}
	c.JSON(http.StatusCreated, thread)
}

// GET /api/v1/threads?limit=20&before=<timestamp_unix>
func (h *Handler) ListFeed(c *gin.Context) {
	userID := c.GetString("user_id")
	limit := 20
	before := time.Now()

	threads, err := h.service.ListFeed(c.Request.Context(), userID, limit, before)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list feed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load feed"})
		return
	}
	if threads == nil {
		threads = []Thread{}
	}
	c.JSON(http.StatusOK, threads)
}

// GET /api/v1/threads/:id
func (h *Handler) GetThread(c *gin.Context) {
	userID := c.GetString("user_id")
	threadID := c.Param("id")
	thread, err := h.service.GetThread(c.Request.Context(), threadID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get post"})
		return
	}
	if thread == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Post not found"})
		return
	}
	c.JSON(http.StatusOK, thread)
}

// POST /api/v1/threads/:id/react
type ReactRequest struct {
	Emoji string `json:"emoji" binding:"required"`
}

func (h *Handler) React(c *gin.Context) {
	userID := c.GetString("user_id")
	threadID := c.Param("id")
	var req ReactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emoji required"})
		return
	}
	if !AllowedEmoji[req.Emoji] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid emoji. Use: 👍 ❤️ 😂 😮 😢 😡"})
		return
	}
	if err := h.service.ToggleReaction(c.Request.Context(), threadID, userID, req.Emoji); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to react"})
		return
	}
	// Return updated thread
	thread, _ := h.service.GetThread(c.Request.Context(), threadID, userID)
	c.JSON(http.StatusOK, thread)
}

// POST /api/v1/threads/:id/comments
type CommentRequest struct {
	Content string `json:"content" binding:"required,min=1,max=500"`
}

func (h *Handler) AddComment(c *gin.Context) {
	userID := c.GetString("user_id")
	threadID := c.Param("id")
	var req CommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content required (1-500 chars)"})
		return
	}
	comment, err := h.service.AddComment(c.Request.Context(), threadID, userID, req.Content)
	if err != nil {
		log.Error().Err(err).Msg("Failed to add comment")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add comment"})
		return
	}
	c.JSON(http.StatusCreated, comment)
}

// GET /api/v1/threads/:id/comments
func (h *Handler) GetComments(c *gin.Context) {
	threadID := c.Param("id")
	comments, err := h.service.GetComments(c.Request.Context(), threadID, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load comments"})
		return
	}
	c.JSON(http.StatusOK, comments)
}
