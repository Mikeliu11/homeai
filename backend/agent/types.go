// Package agent runs one chat turn: it reads what Jordan gave, calls Claude
// with tools, and leaves proposals behind. It never confirms anything.
package agent

import (
	"context"
	"time"

	"helperpay/records"
)

// Agent runs a queued turn to completion (done or failed).
type Agent interface {
	RunTurn(ctx context.Context, turnID string) error
}

// Blobs is where uploaded screenshots live (S3 in prod, memory locally).
type Blobs interface {
	Get(ctx context.Context, key string) ([]byte, string, error) // bytes, content type
}

// MemoryRecord is a long-term memory the assistant holds about the household.
type MemoryRecord struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	Namespace string  `json:"namespace"`
	CreatedAt string  `json:"createdAt,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

// Event is one short-term conversation event.
type Event struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}

// MemoryClient is AgentCore Memory (or the local stand-in).
type MemoryClient interface {
	AddEvent(ctx context.Context, sessionID, clientToken, role, text string) error
	RecentEvents(ctx context.Context, sessionID string, n int) ([]Event, error)
	Retrieve(ctx context.Context, query string, k int) ([]MemoryRecord, error)
	Remember(ctx context.Context, clientToken, text string) error
	List(ctx context.Context) ([]MemoryRecord, error)
	// Forget deletes every event and memory record for the actor (household reset).
	Forget(ctx context.Context) error
	// ForgetSession deletes one conversation's events and summary.
	ForgetSession(ctx context.Context, sessionID string) error
}

// ---- neutral LLM types, so the tool loop is testable without the SDK ----

type ContentBlock struct {
	Text       string          `json:"text,omitempty"`
	ImageBytes []byte          `json:"-"`
	ImageType  string          `json:"imageType,omitempty"` // png | jpeg | gif | webp
	ToolUse    *ToolUse        `json:"toolUse,omitempty"`
	ToolResult *ToolResult     `json:"toolResult,omitempty"`
	Reasoning  *ReasoningBlock `json:"reasoning,omitempty"` // passed back unchanged
}

type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type ToolResult struct {
	ToolUseID string `json:"toolUseId"`
	Text      string `json:"text"`
	IsError   bool   `json:"isError,omitempty"`
}

type ReasoningBlock struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Redacted  []byte `json:"redacted,omitempty"`
}

type Message struct {
	Role    string         `json:"role"` // user | assistant
	Content []ContentBlock `json:"content"`
}

type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

type ConverseRequest struct {
	System   string
	Messages []Message
	Tools    []ToolSpec
}

type ConverseResponse struct {
	Message    Message
	StopReason string // end_turn | tool_use | max_tokens
}

// LLM is the model boundary.
type LLM interface {
	Converse(ctx context.Context, req ConverseRequest) (ConverseResponse, error)
}

// Deps wires a Runner.
type Deps struct {
	Store   records.Store
	Machine *records.Machine
	Blobs   Blobs
	Memory  MemoryClient
	LLM     LLM
	Loc     *time.Location
	Now     func() time.Time
	Actor   string // memory actor id
}
