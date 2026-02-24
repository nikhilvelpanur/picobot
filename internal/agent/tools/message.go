package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/local/picobot/internal/chat"
)

// MessageTool sends messages to a channel via the chat Hub.
// It holds a default context (channel + chatID) set per-incoming-message,
// but allows explicit overrides so system channels (heartbeat, cron) can
// target specific users on interactive channels like Telegram.
type MessageTool struct {
	hub     *chat.Hub
	channel string
	chatID  string
}

func NewMessageTool(b *chat.Hub) *MessageTool {
	return &MessageTool{hub: b}
}

func (m *MessageTool) Name() string { return "message" }
func (m *MessageTool) Description() string {
	return "Send a message to a channel/chat. By default sends to the current conversation. Use optional 'channel' and 'chat_id' to send to a different user (e.g. proactive DMs from heartbeat/cron)."
}

func (m *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Target channel (e.g. 'telegram'). If omitted, uses the current conversation's channel.",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Target chat ID (e.g. a Telegram user ID). If omitted, uses the current conversation's chat ID.",
			},
		},
		"required": []string{"content"},
	}
}

// SetContext sets the current channel and chat id for outgoing messages.
func (m *MessageTool) SetContext(channel, chatID string) {
	m.channel = channel
	m.chatID = chatID
}

// Expected args: {"content": "...", "channel": "...", "chat_id": "..."}
func (m *MessageTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	content := ""
	if c, ok := args["content"]; ok {
		switch v := c.(type) {
		case string:
			content = v
		default:
			b, _ := json.Marshal(v)
			content = string(b)
		}
	}
	if content == "" {
		return "", fmt.Errorf("message tool: 'content' argument required")
	}

	// Use explicit channel/chatID if provided, otherwise fall back to context
	channel := m.channel
	chatID := m.chatID
	if ch, ok := args["channel"].(string); ok && ch != "" {
		channel = ch
	}
	if cid, ok := args["chat_id"].(string); ok && cid != "" {
		chatID = cid
	}

	if channel == "" {
		return "", fmt.Errorf("message tool: no target channel (set via context or 'channel' parameter)")
	}

	// Publish outbound message to hub
	out := chat.Outbound{
		Channel: channel,
		ChatID:  chatID,
		Content: content,
	}
	select {
	case m.hub.Out <- out:
		return fmt.Sprintf("sent to %s:%s", channel, chatID), nil
	default:
		return "", fmt.Errorf("outbound channel full")
	}
}
