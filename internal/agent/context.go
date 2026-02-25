package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/picobot/internal/agent/memory"
	"github.com/local/picobot/internal/agent/skills"
	"github.com/local/picobot/internal/providers"
)

// ContextBuilder builds messages for the LLM from session history and current message.
type ContextBuilder struct {
	workspace    string
	ranker       memory.Ranker
	topK         int
	skillsLoader *skills.Loader
}

func NewContextBuilder(workspace string, r memory.Ranker, topK int) *ContextBuilder {
	return &ContextBuilder{
		workspace:    workspace,
		ranker:       r,
		topK:         topK,
		skillsLoader: skills.NewLoader(workspace),
	}
}

func (cb *ContextBuilder) BuildMessages(history []string, currentMessage string, channel, chatID string, memoryContext string, memories []memory.MemoryItem) []providers.Message {
	msgs := make([]providers.Message, 0, len(history)+8)
	// system prompt with current date/time grounding
	now := time.Now()
	msgs = append(msgs, providers.Message{Role: "system", Content: fmt.Sprintf(
		"You are Picobot, a helpful assistant.\n\nCurrent date and time: %s (UTC: %s)",
		now.Format("Monday, January 2, 2006 3:04 PM MST"),
		now.UTC().Format("Monday, January 2, 2006 15:04 UTC"),
	)})

	// Load workspace bootstrap files (SOUL.md, AGENTS.md, USER.md, TOOLS.md)
	// These define the agent's personality, instructions, and available tools documentation.
	bootstrapFiles := []string{"SOUL.md", "AGENTS.md", "USER.md", "TOOLS.md"}
	for _, name := range bootstrapFiles {
		p := filepath.Join(cb.workspace, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue // file may not exist yet, skip silently
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			msgs = append(msgs, providers.Message{Role: "system", Content: fmt.Sprintf("## %s\n\n%s", name, content)})
		}
	}

	// Tell the model which channel it is operating in and that tools are always available.
	msgs = append(msgs, providers.Message{Role: "system", Content: fmt.Sprintf(
		"You are operating on channel=%q chatID=%q. You have full access to all registered tools regardless of the channel. Always use your tools when the user asks you to perform actions (file operations, shell commands, web fetches, etc.).",
		channel, chatID)})

	// instruction for memory tool usage
	msgs = append(msgs, providers.Message{Role: "system", Content: "If you decide something should be remembered, call the tool 'write_memory' with JSON arguments: {\"target\": \"today\"|\"long\", \"content\": \"...\", \"append\": true|false}. Use a tool call rather than plain chat text when writing memory."})

	// Load skills selectively: only include full content for skills relevant to
	// the current message. Other skills get a one-line summary so the model knows
	// they exist but they don't bloat the context window.
	loadedSkills, err := cb.skillsLoader.LoadAll()
	if err != nil {
		log.Printf("error loading skills: %v", err)
	}
	if len(loadedSkills) > 0 {
		msgLower := strings.ToLower(currentMessage)
		// Score each skill by keyword overlap with the message
		type scored struct {
			skill skills.Skill
			score int
		}
		var scoredSkills []scored
		for _, skill := range loadedSkills {
			s := 0
			nameLower := strings.ToLower(skill.Name)
			// Exact name mention is a strong signal
			if strings.Contains(msgLower, nameLower) {
				s += 10
			}
			// Check description words for weaker signal
			for _, word := range strings.Fields(strings.ToLower(skill.Description)) {
				if len(word) > 3 && strings.Contains(msgLower, word) {
					s++
				}
			}
			scoredSkills = append(scoredSkills, scored{skill, s})
		}
		// Sort by score descending (simple selection for top 2)
		const maxFullSkills = 2
		for i := 0; i < len(scoredSkills) && i < maxFullSkills; i++ {
			for j := i + 1; j < len(scoredSkills); j++ {
				if scoredSkills[j].score > scoredSkills[i].score {
					scoredSkills[i], scoredSkills[j] = scoredSkills[j], scoredSkills[i]
				}
			}
		}
		var sb strings.Builder
		sb.WriteString("Available Skills:\n")
		for i, ss := range scoredSkills {
			if i < maxFullSkills && ss.score > 0 {
				// Full content for relevant skills
				sb.WriteString(fmt.Sprintf("\n## %s\n%s\n\n%s\n", ss.skill.Name, ss.skill.Description, ss.skill.Content))
			} else {
				// Summary only for non-matching skills
				sb.WriteString(fmt.Sprintf("\n- **%s**: %s\n", ss.skill.Name, ss.skill.Description))
			}
		}
		msgs = append(msgs, providers.Message{Role: "system", Content: sb.String()})
	}

	// include file-based memory context (long-term + today's notes) if present
	if memoryContext != "" {
		msgs = append(msgs, providers.Message{Role: "system", Content: "Memory:\n" + memoryContext})
	}

	// select top-K memories using ranker if available
	selected := memories
	if cb.ranker != nil && len(memories) > 0 {
		selected = cb.ranker.Rank(currentMessage, memories, cb.topK)
	}
	if len(selected) > 0 {
		var sb strings.Builder
		sb.WriteString("Relevant memories:\n")
		for _, m := range selected {
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", m.Text, m.Kind))
		}
		msgs = append(msgs, providers.Message{Role: "system", Content: sb.String()})
	}

	// replay history — parse "role: content" format back into proper roles
	for _, h := range history {
		if len(h) == 0 {
			continue
		}
		role := "user"
		content := h
		if idx := strings.Index(h, ": "); idx > 0 && idx < 12 {
			prefix := h[:idx]
			if prefix == "user" || prefix == "assistant" || prefix == "system" {
				role = prefix
				content = h[idx+2:]
			}
		}
		msgs = append(msgs, providers.Message{Role: role, Content: content})
	}

	// current
	msgs = append(msgs, providers.Message{Role: "user", Content: currentMessage})
	return msgs
}
