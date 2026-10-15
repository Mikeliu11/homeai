package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// LocalMemory is the in-process stand-in for AgentCore Memory used by tests
// and the local dev server. Long-term "extraction" is a crude keyword
// pass so the panel has something to show.
type LocalMemory struct {
	mu      sync.Mutex
	events  map[string][]Event
	records []MemoryRecord
	tokens  map[string]bool
}

func NewLocalMemory() *LocalMemory {
	return &LocalMemory{events: map[string][]Event{}, tokens: map[string]bool{}}
}

func (m *LocalMemory) AddEvent(_ context.Context, sessionID, clientToken, role, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if clientToken != "" && m.tokens[clientToken] {
		return nil
	}
	m.tokens[clientToken] = true
	m.events[sessionID] = append(m.events[sessionID], Event{Role: role, Text: text, At: time.Now().UTC().Format(time.RFC3339)})
	return nil
}

func (m *LocalMemory) RecentEvents(_ context.Context, sessionID string, n int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.events[sessionID]
	if len(ev) > n {
		ev = ev[len(ev)-n:]
	}
	out := make([]Event, len(ev))
	copy(out, ev)
	return out, nil
}

func (m *LocalMemory) Retrieve(_ context.Context, query string, k int) ([]MemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	words := strings.Fields(strings.ToLower(query))
	var out []MemoryRecord
	for _, r := range m.records {
		score := 0.0
		lt := strings.ToLower(r.Text)
		for _, w := range words {
			if len(w) > 3 && strings.Contains(lt, w) {
				score++
			}
		}
		if score > 0 {
			r.Score = score
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func (m *LocalMemory) Remember(_ context.Context, clientToken, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if clientToken != "" && m.tokens[clientToken] {
		return nil
	}
	m.tokens[clientToken] = true
	m.records = append(m.records, MemoryRecord{ID: clientToken, Text: text, Namespace: "/household/local/facts/", CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	return nil
}

func (m *LocalMemory) List(_ context.Context) ([]MemoryRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MemoryRecord, len(m.records))
	copy(out, m.records)
	return out, nil
}

func (m *LocalMemory) Forget(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events, m.records, m.tokens = map[string][]Event{}, nil, map[string]bool{}
	return nil
}

func (m *LocalMemory) ForgetSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.events, sessionID)
	return nil
}
