package notification

import (
	"context"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/option"
)

// Service sends FCM push notifications.
//
// DESIGN PRINCIPLE:
// FCM notifications carry ONLY metadata — never media content.
// File bytes, audio, video are NEVER included in FCM payloads.
// FCM only notifies the recipient device to take action (open app, fetch via WS/P2P).

type Service struct {
	client *messaging.Client
}

func NewService(credentialsPath string) *Service {
	app, err := firebase.NewApp(context.Background(), nil,
		option.WithCredentialsFile(credentialsPath))
	if err != nil {
		log.Error().Err(err).Msg("Failed to initialize Firebase")
		return &Service{} // Return empty service — notifications disabled but app still runs
	}

	client, err := app.Messaging(context.Background())
	if err != nil {
		log.Error().Err(err).Msg("Failed to get messaging client")
		return &Service{}
	}

	return &Service{client: client}
}

// ─── Call Notification ─────────────────────────────────────────────────────────

type CallNotif struct {
	CallID     string
	CallerID   string
	CallerName string
	CallType   string  // "audio" | "video" | "group_audio" | "group_video"
	GroupID    string
}

// SendCallNotification — high-priority FCM to wake app for incoming call.
// Payload: call metadata ONLY. No audio/video data.
func (s *Service) SendCallNotification(toUserFCMToken string, notif CallNotif) {
	if s.client == nil || toUserFCMToken == "" {
		return
	}

	msg := &messaging.Message{
		Token: toUserFCMToken,
		Data: map[string]string{
			"type":        "CALL_INVITE",
			"call_id":     notif.CallID,
			"caller_id":   notif.CallerID,
			"caller_name": notif.CallerName,
			"call_type":   notif.CallType,
			"group_id":    notif.GroupID,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",  // Wake device immediately
			TTL:      durationPtr(30 * 1e9), // 30 seconds — call becomes irrelevant after
			Notification: &messaging.AndroidNotification{
				Title:             "Incoming " + notif.CallType + " call",
				Body:              notif.CallerName + " is calling...",
				ChannelID:         "chatbook_calls",
				Priority: messaging.PriorityHigh,
			},
		},
	}

	_, err := s.client.Send(context.Background(), msg)
	if err != nil {
		log.Error().Err(err).Str("callID", notif.CallID).Msg("FCM call notification failed")
	}
}

// ─── Message Notification ──────────────────────────────────────────────────────

// SendMessageNotification — notify recipient of new encrypted message.
// Body says "New message" — actual content is E2E encrypted and not in FCM.
func (s *Service) SendMessageNotification(toFCMToken, fromName, conversationID string) {
	if s.client == nil || toFCMToken == "" {
		return
	}

	msg := &messaging.Message{
		Token: toFCMToken,
		Data: map[string]string{
			"type":            "NEW_MESSAGE",
			"sender_name":     fromName,
			"conversation_id": conversationID,
			// NOTE: message content is NOT included — it's E2E encrypted
		},
		Android: &messaging.AndroidConfig{
			Priority: "normal",
			Notification: &messaging.AndroidNotification{
				Title:     fromName,
				Body:      "New encrypted message", // Actual content decrypted on device only
				ChannelID: "chatbook_messages",
			},
		},
	}

	_, err := s.client.Send(context.Background(), msg)
	if err != nil {
		log.Error().Err(err).Msg("FCM message notification failed")
	}
}

// ─── File Transfer Notification ────────────────────────────────────────────────

// SendFileNotification — notify recipient that a file transfer is ready.
// No file bytes included — just metadata.
func (s *Service) SendFileNotification(toFCMToken, fileName, fromName string) {
	if s.client == nil || toFCMToken == "" {
		return
	}

	msg := &messaging.Message{
		Token: toFCMToken,
		Data: map[string]string{
			"type":        "FILE_NOTIFY",
			"file_name":   fileName,
			"sender_name": fromName,
			// No file content — transfer happens via WebRTC DataChannel
		},
		Android: &messaging.AndroidConfig{
			Priority: "normal",
			Notification: &messaging.AndroidNotification{
				Title:     fromName + " wants to send you a file",
				Body:      fileName,
				ChannelID: "chatbook_messages",
			},
		},
	}

	_, err := s.client.Send(context.Background(), msg)
	if err != nil {
		log.Error().Err(err).Msg("FCM file notification failed")
	}
}

func durationPtr(ns int64) *time.Duration {
	d := time.Duration(ns)
	return &d
}
