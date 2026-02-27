package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/service"
	"go.uber.org/zap"
)

// WebSocket message types for the cooking protocol.
const (
	MsgTypeChatMessage     = "chat_message"     // User sends a cooking Q&A question
	MsgTypeChatResponse    = "chat_response"    // AI responds to cooking Q&A
	MsgTypeEphemeralEdit   = "ephemeral_edit"   // Temporary recipe modification
	MsgTypeEphemeralReset  = "ephemeral_reset"  // Reset ephemeral edits
	MsgTypeVoiceTranscript = "voice_transcript" // Audio transcription result
	MsgTypeVoiceIntent     = "voice_intent"     // Classified voice intent
	MsgTypeScrollCommand   = "scroll_command"   // Voice-driven scroll
	MsgTypeNavigateCommand = "navigate_command" // Voice-driven navigation
	MsgTypeError           = "error"            // Error message
	MsgTypeConnected       = "connected"        // Connection confirmed
)

// WSMessage is the envelope for all messages sent over the cooking WebSocket.
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ChatMessagePayload is sent by the client to ask a cooking question.
type ChatMessagePayload struct {
	Message       string `json:"message"`
	RecipeContext string `json:"recipe_context,omitempty"`
}

// ChatResponsePayload is sent by the server with an AI answer.
type ChatResponsePayload struct {
	Message string `json:"message"`
}

// EphemeralEditPayload represents a temporary recipe modification.
type EphemeralEditPayload struct {
	StepIndex    int    `json:"step_index,omitempty"`
	Modification string `json:"modification"`
}

// VoiceTranscriptPayload carries a transcription from audio input.
type VoiceTranscriptPayload struct {
	Transcript string `json:"transcript"`
	AudioData  []byte `json:"audio_data,omitempty"` // base64-encoded
}

// VoiceIntentPayload carries the classified intent of a voice command.
type VoiceIntentPayload struct {
	Type   string `json:"type"`             // scroll_up, scroll_down, navigate, question, ignore
	Amount string `json:"amount,omitempty"` // small, large
	Target string `json:"target,omitempty"` // ingredients, instructions
	Text   string `json:"text,omitempty"`   // for question type
}

// ScrollCommandPayload drives voice-driven scrolling.
type ScrollCommandPayload struct {
	Direction string `json:"direction"` // up, down
	Amount    string `json:"amount"`    // small, medium, large
}

// NavigateCommandPayload drives voice-driven navigation.
type NavigateCommandPayload struct {
	Target string `json:"target"` // ingredients, instructions, step_N
}

// ErrorPayload carries an error message to the client.
type ErrorPayload struct {
	Message string `json:"message"`
}

// ConnectedPayload confirms a successful connection.
type ConnectedPayload struct {
	RecipeID string `json:"recipe_id"`
	UserID   uint   `json:"user_id"`
}

// CookingHandler manages WebSocket connections for cooking mode.
type CookingHandler struct {
	Hub          *Hub
	JwtSecret    string
	VoiceService *service.VoiceService
}

// NewCookingHandler returns a new CookingHandler.
func NewCookingHandler(hub *Hub, jwtSecret string, voiceService *service.VoiceService) *CookingHandler {
	return &CookingHandler{
		Hub:          hub,
		JwtSecret:    jwtSecret,
		VoiceService: voiceService,
	}
}

// upgrader is configured for cooking mode WebSocket upgrades.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		switch origin {
		case "https://saltybytes.ai",
			"https://www.saltybytes.ai",
			"https://api.saltybytes.ai":
			return true
		}
		// Allow localhost for development
		if strings.HasPrefix(origin, "http://localhost:") || origin == "http://localhost" {
			return true
		}
		return false
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// HandleCookingSession upgrades an HTTP request to a WebSocket connection
// for cooking mode. Authentication is done via a "token" query parameter
// because WebSocket connections cannot easily use Authorization headers.
func (ch *CookingHandler) HandleCookingSession(c *gin.Context) {
	log := logger.Get()

	recipeID := c.Param("recipe_id")
	if recipeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "recipe_id is required"})
		return
	}

	// Authenticate via query param token
	tokenString := c.Query("token")
	if tokenString == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "token query parameter is required"})
		return
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(ch.JwtSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid or expired token"})
		return
	}

	// Ensure this is an access token
	tokenType, ok := claims["type"].(string)
	if !ok || tokenType != "access" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid token type"})
		return
	}

	// Extract user ID
	idFloat, ok := claims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid user_id in token"})
		return
	}
	userID := uint(idFloat)

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Error("websocket upgrade failed",
			zap.String("recipe_id", recipeID),
			zap.Uint("user_id", userID),
			zap.Error(err),
		)
		return
	}

	// Create client and register with hub
	client := &Client{
		Hub:    ch.Hub,
		Conn:   conn,
		Send:   make(chan []byte, 256),
		RoomID: recipeID,
		UserID: userID,
	}
	ch.Hub.Register <- client

	// Send connected confirmation
	connectedPayload, _ := json.Marshal(ConnectedPayload{
		RecipeID: recipeID,
		UserID:   userID,
	})
	connectedMsg, _ := json.Marshal(WSMessage{
		Type:    MsgTypeConnected,
		Payload: connectedPayload,
	})
	client.Send <- connectedMsg

	log.Info("cooking session started",
		zap.String("recipe_id", recipeID),
		zap.Uint("user_id", userID),
	)

	// Start read and write pumps
	go client.WritePump()
	go client.ReadPump(func(cl *Client, data []byte) {
		ch.handleMessage(cl, data)
	})
}

// handleMessage parses an incoming WebSocket message and routes it to the
// appropriate handler.
func (ch *CookingHandler) handleMessage(client *Client, data []byte) {
	log := logger.Get()

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		ch.sendError(client, "invalid message format")
		return
	}

	log.Debug("received ws message",
		zap.String("type", msg.Type),
		zap.String("room_id", client.RoomID),
		zap.Uint("user_id", client.UserID),
	)

	switch msg.Type {
	case MsgTypeChatMessage:
		ch.handleChatMessage(client, msg.Payload)

	case MsgTypeEphemeralEdit:
		// Broadcast ephemeral edit to all clients in the room
		ch.Hub.Broadcast <- &RoomMessage{
			RoomID:  client.RoomID,
			Message: data,
			Sender:  nil, // send to everyone including sender
		}

	case MsgTypeEphemeralReset:
		// Broadcast reset to all clients in the room
		ch.Hub.Broadcast <- &RoomMessage{
			RoomID:  client.RoomID,
			Message: data,
			Sender:  nil,
		}

	case MsgTypeVoiceTranscript:
		ch.handleVoiceTranscript(client, msg.Payload)

	default:
		ch.sendError(client, "unknown message type: "+msg.Type)
	}
}

// handleChatMessage processes a cooking Q&A question.
func (ch *CookingHandler) handleChatMessage(client *Client, payload json.RawMessage) {
	log := logger.Get()

	var chatMsg ChatMessagePayload
	if err := json.Unmarshal(payload, &chatMsg); err != nil {
		ch.sendError(client, "invalid chat message payload")
		return
	}

	if chatMsg.Message == "" {
		ch.sendError(client, "message cannot be empty")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Info("answering cooking question",
		zap.String("room_id", client.RoomID),
		zap.Uint("user_id", client.UserID),
	)

	answer, err := ch.VoiceService.AnswerCookingQuestion(ctx, chatMsg.Message, chatMsg.RecipeContext)
	if err != nil {
		log.Error("failed to get cooking answer",
			zap.String("room_id", client.RoomID),
			zap.Uint("user_id", client.UserID),
			zap.Error(err),
		)
		ch.sendError(client, "failed to get cooking answer")
		return
	}

	responsePayload, _ := json.Marshal(ChatResponsePayload{
		Message: answer,
	})
	responseMsg, _ := json.Marshal(WSMessage{
		Type:    MsgTypeChatResponse,
		Payload: responsePayload,
	})
	client.Send <- responseMsg
}

// handleVoiceTranscript processes a voice transcription.
func (ch *CookingHandler) handleVoiceTranscript(client *Client, payload json.RawMessage) {
	log := logger.Get()

	var transcript VoiceTranscriptPayload
	if err := json.Unmarshal(payload, &transcript); err != nil {
		ch.sendError(client, "invalid voice transcript payload")
		return
	}

	if transcript.Transcript == "" && len(transcript.AudioData) == 0 {
		ch.sendError(client, "transcript or audio_data is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var intent *ai.VoiceIntent
	var err error

	if len(transcript.AudioData) > 0 {
		log.Info("processing voice command from audio",
			zap.String("room_id", client.RoomID),
			zap.Uint("user_id", client.UserID),
		)
		intent, err = ch.VoiceService.ProcessVoiceCommand(ctx, transcript.AudioData)
	} else {
		log.Info("classifying voice intent from text",
			zap.String("room_id", client.RoomID),
			zap.Uint("user_id", client.UserID),
		)
		intent, err = ch.VoiceService.TextProvider.ClassifyVoiceIntent(ctx, transcript.Transcript)
	}
	if err != nil {
		log.Error("failed to process voice transcript",
			zap.String("room_id", client.RoomID),
			zap.Uint("user_id", client.UserID),
			zap.Error(err),
		)
		ch.sendError(client, "failed to process voice command")
		return
	}

	// Always send the raw classified intent
	intentPayload, _ := json.Marshal(VoiceIntentPayload{
		Type:   intent.Type,
		Amount: intent.Amount,
		Target: intent.Target,
		Text:   intent.Text,
	})
	intentMsg, _ := json.Marshal(WSMessage{
		Type:    MsgTypeVoiceIntent,
		Payload: intentPayload,
	})
	client.Send <- intentMsg

	// Map intent to specific command messages
	switch intent.Type {
	case "scroll_up", "scroll_down":
		direction := "down"
		if intent.Type == "scroll_up" {
			direction = "up"
		}
		scrollPayload, _ := json.Marshal(ScrollCommandPayload{
			Direction: direction,
			Amount:    intent.Amount,
		})
		scrollMsg, _ := json.Marshal(WSMessage{
			Type:    MsgTypeScrollCommand,
			Payload: scrollPayload,
		})
		client.Send <- scrollMsg

	case "navigate":
		navPayload, _ := json.Marshal(NavigateCommandPayload{
			Target: intent.Target,
		})
		navMsg, _ := json.Marshal(WSMessage{
			Type:    MsgTypeNavigateCommand,
			Payload: navPayload,
		})
		client.Send <- navMsg

	case "question":
		answer, err := ch.VoiceService.AnswerCookingQuestion(ctx, intent.Text, "")
		if err != nil {
			log.Error("failed to answer voice question",
				zap.String("room_id", client.RoomID),
				zap.Uint("user_id", client.UserID),
				zap.Error(err),
			)
			ch.sendError(client, "failed to get cooking answer")
			return
		}
		chatPayload, _ := json.Marshal(ChatResponsePayload{
			Message: answer,
		})
		chatMsg, _ := json.Marshal(WSMessage{
			Type:    MsgTypeChatResponse,
			Payload: chatPayload,
		})
		client.Send <- chatMsg

	case "ignore":
		// Do nothing

	default:
		log.Warn("unknown voice intent type",
			zap.String("intent_type", intent.Type),
			zap.String("room_id", client.RoomID),
		)
	}
}

// sendError sends an error message to a single client.
func (ch *CookingHandler) sendError(client *Client, message string) {
	errPayload, _ := json.Marshal(ErrorPayload{
		Message: message,
	})
	errMsg, _ := json.Marshal(WSMessage{
		Type:    MsgTypeError,
		Payload: errPayload,
	})
	client.Send <- errMsg
}
