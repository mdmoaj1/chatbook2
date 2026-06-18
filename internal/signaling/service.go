package signaling

import (
	"encoding/json"

	"github.com/chatbook/backend/internal/notification"
	"github.com/chatbook/backend/internal/websocket"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Service handles WebRTC signaling — pure relay, no media processing.
//
// Design principle: The signaling service ONLY exchanges SDP and ICE candidates
// between peers to help them establish direct P2P connections.
// Once P2P is established, ALL audio/video/data flows between clients directly.
// The backend carries ZERO media bandwidth.

type Service struct {
	hub              *websocket.Hub
	notificationSvc  *notification.Service
}

func NewService(hub *websocket.Hub, notifSvc *notification.Service) *Service {
	return &Service{
		hub:             hub,
		notificationSvc: notifSvc,
	}
}

// ─── Call Signaling via WebSocket ─────────────────────────────────────────────

// HandleCallInvite — Called when user A wants to call user B (or group).
// Backend:
//   1. Forwards CALL_INVITE to recipient(s) via WebSocket
//   2. If recipient is offline, sends FCM push notification to wake the app
//   3. Does NOT establish any media relay — media is purely P2P
func (s *Service) HandleCallInvite(fromUserID string, env websocket.Envelope) {
	var payload websocket.CallPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Error().Err(err).Msg("Invalid call invite payload")
		return
	}

	if payload.GroupID != "" {
		// Group call — invite all participants
		s.handleGroupCallInvite(fromUserID, payload, env)
		return
	}

	// 1:1 call
	delivered := s.hub.SendToUser(env.To, env)
	if !delivered {
		// Recipient is offline — send FCM to wake app
		s.notificationSvc.SendCallNotification(env.To, notification.CallNotif{
			CallID:     payload.CallID,
			CallerID:   fromUserID,
			CallerName: "", // Populated by notification service from DB
			CallType:   payload.CallType,
		})
		log.Info().Str("to", env.To).Msg("Call invite: recipient offline, FCM sent")
	}
}

// HandleSignalForward — Simply forwards SDP/ICE between peers.
// No parsing or processing of media content.
func (s *Service) HandleSignalForward(fromUserID string, env websocket.Envelope) {
	// Pure relay — just forward to the destination peer
	if env.To == "" {
		log.Warn().Str("from", fromUserID).Msg("Signal forward: missing 'to' field")
		return
	}

	delivered := s.hub.SendToUser(env.To, env)
	if !delivered {
		log.Warn().
			Str("from", fromUserID).
			Str("to", env.To).
			Str("type", string(env.Type)).
			Msg("Signal forward: peer offline")
	}
}

// HandleCallEnd — Notify all participants that call has ended.
func (s *Service) HandleCallEnd(fromUserID string, env websocket.Envelope) {
	s.hub.SendToUser(env.To, env)
}

// ─── Group Call (Full-Mesh) ───────────────────────────────────────────────────

// handleGroupCallInvite — For group calls, each participant forms a direct P2P
// connection with every other participant (full mesh).
// Backend only facilitates participant discovery and signaling relay.
// No centralized media mixing or SFU.
// Practical limit: up to 8 participants (28 P2P connections max).
func (s *Service) handleGroupCallInvite(fromUserID string, payload websocket.CallPayload, env websocket.Envelope) {
	log.Info().
		Str("groupID", payload.GroupID).
		Int("participants", len(payload.Participants)).
		Msg("Group call invite")

	// Notify each participant
	for _, participantID := range payload.Participants {
		if participantID == fromUserID {
			continue
		}

		inviteEnv := websocket.Envelope{
			Type:    websocket.TypeGroupCallInvite,
			ID:      uuid.NewString(),
			From:    fromUserID,
			To:      participantID,
			GroupID: payload.GroupID,
			Payload: env.Payload,
		}

		delivered := s.hub.SendToUser(participantID, inviteEnv)
		if !delivered {
			// Send FCM push to wake app
			s.notificationSvc.SendCallNotification(participantID, notification.CallNotif{
				CallID:   payload.CallID,
				CallerID: fromUserID,
				CallType: payload.CallType,
				GroupID:  payload.GroupID,
			})
		}
	}
}

// HandleGroupSignalForward — Forward mesh signaling between group participants.
// In full-mesh, every participant generates an SDP offer for every other participant.
// Backend merely relays these offers/answers/ICE candidates.
func (s *Service) HandleGroupSignalForward(fromUserID string, env websocket.Envelope) {
	// Relay to the specific peer within the group
	s.hub.SendToUser(env.To, env)
}

// HandleFileTransferNotify — Notify peer about an incoming file transfer.
// ONLY the metadata is sent through backend. File bytes travel via WebRTC DataChannel.
func (s *Service) HandleFileTransferNotify(fromUserID string, env websocket.Envelope) {
	var payload websocket.FileTransferNotifyPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Error().Err(err).Msg("Invalid file transfer notify payload")
		return
	}

	log.Info().
		Str("from", fromUserID).
		Str("to", env.To).
		Str("fileName", payload.FileName).
		Int64("fileSize", payload.FileSize).
		Msg("File transfer notify — metadata only, no bytes stored")

	// Just notify the recipient — actual file transfer is P2P
	delivered := s.hub.SendToUser(env.To, env)
	if !delivered {
		// Recipient is offline — store notification, file transfer will start when online
		s.notificationSvc.SendFileNotification(env.To, payload.FileName, fromUserID)
	}
}
