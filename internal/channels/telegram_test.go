package channels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/local/picobot/internal/chat"
)

func TestStartTelegramWithBase(t *testing.T) {
	token := "testtoken"
	// channel to capture sendMessage posts
	sent := make(chan url.Values, 4)

	// simple stateful handler: first getUpdates returns one update, subsequent return empty
	first := true
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/getUpdates") {
			w.Header().Set("Content-Type", "application/json")
			if first {
				first = false
				w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"from":{"id":123},"chat":{"id":456,"type":"private"},"text":"hello"}}]}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		if strings.HasSuffix(path, "/sendMessage") {
			r.ParseForm()
			sent <- r.PostForm
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer h.Close()

	base := h.URL + "/bot" + token
	b := chat.NewHub(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := StartTelegramWithBase(ctx, b, token, base, nil); err != nil {
		t.Fatalf("StartTelegramWithBase failed: %v", err)
	}
	// Start the hub router so outbound messages sent to b.Out are dispatched
	// to each channel's subscription (telegram in this test).
	b.StartRouter(ctx)

	// Wait for inbound from getUpdates
	select {
	case msg := <-b.In:
		if msg.Content != "hello" {
			t.Fatalf("unexpected inbound content: %s", msg.Content)
		}
		if msg.ChatID != "456" {
			t.Fatalf("unexpected chat id: %s", msg.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound message")
	}

	// send an outbound message and ensure server receives it
	out := chat.Outbound{Channel: "telegram", ChatID: "456", Content: "reply"}
	b.Out <- out

	select {
	case v := <-sent:
		if v.Get("chat_id") != "456" || v.Get("text") != "reply" {
			t.Fatalf("unexpected sendMessage form: %v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for sendMessage to be posted")
	}

	// cancel and allow goroutines to stop
	cancel()
	// give a small grace period
	time.Sleep(50 * time.Millisecond)
}

func TestTelegramSendMarkdownFallback(t *testing.T) {
	// Server rejects Markdown parse_mode with 400, accepts plain text.
	calls := make(chan url.Values, 4)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		calls <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("parse_mode") == "Markdown" {
			w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer h.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	out := chat.Outbound{Channel: "telegram", ChatID: "789", Content: "*   **bold bullet**"}
	telegramSend(client, h.URL, out)

	// First call should be with Markdown
	select {
	case v := <-calls:
		if v.Get("parse_mode") != "Markdown" {
			t.Fatalf("expected first call with Markdown, got parse_mode=%q", v.Get("parse_mode"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first sendMessage call")
	}

	// Second call should be without parse_mode (plain text fallback)
	select {
	case v := <-calls:
		if v.Get("parse_mode") != "" {
			t.Fatalf("expected fallback without parse_mode, got %q", v.Get("parse_mode"))
		}
		if v.Get("chat_id") != "789" {
			t.Fatalf("expected chat_id 789, got %q", v.Get("chat_id"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for plaintext fallback call")
	}
}

func TestTelegramSendSuccess(t *testing.T) {
	calls := make(chan url.Values, 2)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		calls <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer h.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	out := chat.Outbound{Channel: "telegram", ChatID: "123", Content: "hello world"}
	telegramSend(client, h.URL, out)

	// Should succeed on first try, only one call
	select {
	case v := <-calls:
		if v.Get("chat_id") != "123" || v.Get("text") != "hello world" {
			t.Fatalf("unexpected form values: %v", v)
		}
		if v.Get("parse_mode") != "Markdown" {
			t.Fatalf("expected Markdown parse_mode, got %q", v.Get("parse_mode"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	// No second call
	select {
	case <-calls:
		t.Fatal("unexpected second call — should have succeeded on first try")
	case <-time.After(100 * time.Millisecond):
		// good, no second call
	}
}
