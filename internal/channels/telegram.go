package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/local/picobot/internal/chat"
)

// StartTelegram is a convenience wrapper that uses the real polling implementation
// with the standard Telegram base URL.
// allowFrom is a list of Telegram user IDs permitted to interact with the bot.
// If empty, ALL users are allowed (open mode).
func StartTelegram(ctx context.Context, hub *chat.Hub, token string, allowFrom []string) error {
	if token == "" {
		return fmt.Errorf("telegram token not provided")
	}
	base := "https://api.telegram.org/bot" + token
	return StartTelegramWithBase(ctx, hub, token, base, allowFrom)
}

// StartTelegramWithBase starts long-polling against the given base URL (e.g., https://api.telegram.org/bot<TOKEN> or a test server URL).
// allowFrom restricts which Telegram user IDs may send messages. Empty means allow all.
func StartTelegramWithBase(ctx context.Context, hub *chat.Hub, token, base string, allowFrom []string) error {
	if base == "" {
		return fmt.Errorf("base URL is required")
	}

	// Build a fast lookup set for allowed user IDs.
	allowed := make(map[string]struct{}, len(allowFrom))
	for _, id := range allowFrom {
		allowed[id] = struct{}{}
	}

	client := &http.Client{Timeout: 45 * time.Second}

	// inbound polling goroutine
	go func() {
		offset := int64(0)
		for {
			select {
			case <-ctx.Done():
				log.Println("telegram: stopping inbound polling")
				return
			default:
			}

			values := url.Values{}
			values.Set("offset", strconv.FormatInt(offset, 10))
			values.Set("timeout", "30")
			u := base + "/getUpdates"
			resp, err := client.PostForm(u, values)
			if err != nil {
				log.Printf("telegram getUpdates error: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var gu struct {
				Ok     bool `json:"ok"`
				Result []struct {
					UpdateID int64 `json:"update_id"`
					Message  *struct {
						MessageID int64 `json:"message_id"`
						From      *struct {
							ID int64 `json:"id"`
						} `json:"from"`
						Chat struct {
							ID int64 `json:"id"`
						} `json:"chat"`
						Text string `json:"text"`
					} `json:"message"`
				} `json:"result"`
			}
			if err := json.Unmarshal(body, &gu); err != nil {
				log.Printf("telegram: invalid getUpdates response: %v", err)
				continue
			}
			for _, upd := range gu.Result {
				if upd.UpdateID >= offset {
					offset = upd.UpdateID + 1
				}
				if upd.Message == nil {
					continue
				}
				m := upd.Message
				fromID := ""
				if m.From != nil {
					fromID = strconv.FormatInt(m.From.ID, 10)
				}
				// Enforce allowFrom: if the list is non-empty, reject unknown senders.
				if len(allowed) > 0 {
					if _, ok := allowed[fromID]; !ok {
						log.Printf("telegram: dropping message from unauthorized user %s", fromID)
						continue
					}
				}
				chatID := strconv.FormatInt(m.Chat.ID, 10)
				hub.In <- chat.Inbound{
					Channel:   "telegram",
					SenderID:  fromID,
					ChatID:    chatID,
					Content:   m.Text,
					Timestamp: time.Now(),
				}
			}
		}
	}()

	// Subscribe to the outbound queue before launching the goroutine so the
	// registration is visible to the hub router from the moment this function returns.
	outCh := hub.Subscribe("telegram")

	// outbound sender goroutine
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		for {
			select {
			case <-ctx.Done():
				log.Println("telegram: stopping outbound sender")
				return
			case out := <-outCh:
				telegramSend(client, base, out)
			}
		}
	}()

	return nil
}

// telegramAPIResponse is the minimal shape of a Telegram Bot API response.
type telegramAPIResponse struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after,omitempty"`
	} `json:"parameters,omitempty"`
}

// telegramSend sends an outbound message via the Telegram Bot API with:
//   - Markdown parse mode as first attempt
//   - Automatic plain-text fallback on 400 (Markdown parse errors)
//   - Retry with backoff on 429 (rate limit)
//   - Up to 2 retries on 5xx server errors
//   - Full delivery logging
func telegramSend(client *http.Client, base string, out chat.Outbound) {
	u := base + "/sendMessage"

	// First attempt: with Markdown parse mode.
	v := url.Values{}
	v.Set("chat_id", out.ChatID)
	v.Set("text", out.Content)
	v.Set("parse_mode", "Markdown")

	apiResp, err := doTelegramPost(client, u, v)
	if err != nil {
		log.Printf("telegram: send to %s failed (network): %v", out.ChatID, err)
		return
	}
	if apiResp.Ok {
		log.Printf("telegram: delivered to %s (%d chars)", out.ChatID, len(out.Content))
		return
	}

	// 400 — almost always a Markdown parse error. Retry as plain text.
	if apiResp.ErrorCode == 400 {
		log.Printf("telegram: markdown rejected for %s (%s), retrying as plain text", out.ChatID, apiResp.Description)
		v.Del("parse_mode")
		apiResp, err = doTelegramPost(client, u, v)
		if err != nil {
			log.Printf("telegram: plaintext retry to %s failed (network): %v", out.ChatID, err)
			return
		}
		if apiResp.Ok {
			log.Printf("telegram: delivered to %s as plain text (%d chars)", out.ChatID, len(out.Content))
			return
		}
		log.Printf("telegram: plaintext retry to %s failed: [%d] %s", out.ChatID, apiResp.ErrorCode, apiResp.Description)
		return
	}

	// 429 — rate limited. Wait the requested duration and retry once.
	if apiResp.ErrorCode == 429 {
		wait := 5
		if apiResp.Parameters != nil && apiResp.Parameters.RetryAfter > 0 {
			wait = apiResp.Parameters.RetryAfter
		}
		log.Printf("telegram: rate limited for %s, waiting %ds", out.ChatID, wait)
		time.Sleep(time.Duration(wait) * time.Second)
		apiResp, err = doTelegramPost(client, u, v)
		if err != nil {
			log.Printf("telegram: retry after rate limit to %s failed (network): %v", out.ChatID, err)
			return
		}
		if apiResp.Ok {
			log.Printf("telegram: delivered to %s after rate-limit wait (%d chars)", out.ChatID, len(out.Content))
			return
		}
		log.Printf("telegram: retry after rate limit to %s failed: [%d] %s", out.ChatID, apiResp.ErrorCode, apiResp.Description)
		return
	}

	// 5xx — server error, retry up to 2 times with backoff.
	if apiResp.ErrorCode >= 500 {
		for attempt := 1; attempt <= 2; attempt++ {
			log.Printf("telegram: server error for %s [%d], retry %d/2", out.ChatID, apiResp.ErrorCode, attempt)
			time.Sleep(time.Duration(attempt) * time.Second)
			apiResp, err = doTelegramPost(client, u, v)
			if err != nil {
				log.Printf("telegram: retry %d to %s failed (network): %v", attempt, out.ChatID, err)
				continue
			}
			if apiResp.Ok {
				log.Printf("telegram: delivered to %s on retry %d (%d chars)", out.ChatID, attempt, len(out.Content))
				return
			}
		}
		log.Printf("telegram: gave up on %s after server errors: [%d] %s", out.ChatID, apiResp.ErrorCode, apiResp.Description)
		return
	}

	// Any other error code — log and move on.
	log.Printf("telegram: send to %s failed: [%d] %s", out.ChatID, apiResp.ErrorCode, apiResp.Description)
}

// doTelegramPost performs the HTTP POST and parses the Telegram API response.
func doTelegramPost(client *http.Client, u string, v url.Values) (telegramAPIResponse, error) {
	var apiResp telegramAPIResponse
	resp, err := client.PostForm(u, v)
	if err != nil {
		return apiResp, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	json.Unmarshal(body, &apiResp)
	return apiResp, nil
}
