package heartbeat

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/picobot/internal/chat"
)

// StartHeartbeat is a no-op. The heartbeat loop is disabled — all deliverables
// (CEO Brief, Company Pulse, etc.) are now on-demand via Telegram.
// The agent still has access to HEARTBEAT.md, memory, and skills, so when the
// user asks for a brief or pulse, it can compose and deliver one immediately.
func StartHeartbeat(ctx context.Context, workspace string, interval time.Duration, hub *chat.Hub) {
	log.Println("heartbeat: disabled (on-demand mode)")
}
