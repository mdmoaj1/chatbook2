package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type Handler struct {
	service *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{service: svc}
}

// ─── Google Sign-In ───────────────────────────────────────────────────────────

type GoogleSignInRequest struct {
	IDToken string `json:"id_token" binding:"required"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         UserDTO `json:"user"`
}

type UserDTO struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatar_url"`
	PublicKey   string `json:"public_key"`
}

// POST /api/v1/auth/google
// Exchange Google ID token for Chatbook JWT.
// Creates user account on first sign-in.
func (h *Handler) GoogleSignIn(c *gin.Context) {
	var req GoogleSignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id_token is required"})
		return
	}

	// Verify Google ID token and get user info
	googleUser, err := h.service.VerifyGoogleToken(c.Request.Context(), req.IDToken)
	if err != nil {
		log.Error().Err(err).Msg("Google token verification failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Google token"})
		return
	}

	// Create or update user in DB
	user, err := h.service.UpsertUser(c.Request.Context(), googleUser)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upsert user")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Generate JWT access + refresh tokens
	accessToken, refreshToken, err := h.service.GenerateTokens(user.ID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate tokens")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}

	// Store refresh token hash in DB
	if err := h.service.StoreRefreshToken(c.Request.Context(), user.ID, refreshToken); err != nil {
		log.Error().Err(err).Msg("Failed to store refresh token")
	}

	c.JSON(http.StatusOK, AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    900, // 15 minutes
		User: UserDTO{
			ID:          user.ID,
			DisplayName: user.DisplayName,
			Email:       user.Email,
			AvatarURL:   user.AvatarURL,
			PublicKey:   user.PublicKey,
		},
	})
}

// ─── Refresh Token ────────────────────────────────────────────────────────────

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// POST /api/v1/auth/refresh
func (h *Handler) RefreshToken(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token required"})
		return
	}

	userID, newAccess, newRefresh, err := h.service.RefreshTokens(c.Request.Context(), req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired refresh token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  newAccess,
		"refresh_token": newRefresh,
		"expires_in":    900,
		"user_id":       userID,
	})
}

// ─── Logout ───────────────────────────────────────────────────────────────────

// POST /api/v1/auth/logout
func (h *Handler) Logout(c *gin.Context) {
	userID := c.GetString("user_id")
	authHeader := c.GetHeader("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")

	if err := h.service.RevokeTokens(c.Request.Context(), userID, token); err != nil {
		log.Error().Err(err).Msg("Failed to revoke tokens")
	}

	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}
