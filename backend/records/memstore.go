package records

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// MemStore is the in-memory Store used by tests and the local dev server.
type MemStore struct {
	mu        sync.Mutex
	helpers   map[string]Helper
	sources   map[string]Source
	terms     map[string]TermsVersion
	bills     map[string]BillRecord
	proposals map[string]Proposal
	turns     map[string]Turn
	audit     []AuditEvent
}

func NewMemStore() *MemStore {
	return &MemStore{helpers: map[string]Helper{}, sources: map[string]Source{}, terms: map[string]TermsVersion{},
		bills: map[string]BillRecord{}, proposals: map[string]Proposal{}, turns: map[string]Turn{}}
}

func clone[T any](v T) T {
	var out T
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &out)
	return out
}

func (m *MemStore) PutHelper(_ context.Context, h Helper) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.helpers[h.ID] = clone(h)
	return nil
}
func (m *MemStore) GetHelper(_ context.Context, id string) (Helper, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.helpers[id]
	if !ok {
		return Helper{}, ErrNotFound
	}
	return clone(h), nil
}
func (m *MemStore) ListHelpers(_ context.Context) ([]Helper, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Helper
	for _, h := range m.helpers {
		out = append(out, clone(h))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *MemStore) PutSource(_ context.Context, s Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sources[s.ID] = clone(s)
	return nil
}
func (m *MemStore) GetSource(_ context.Context, id string) (Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	return clone(s), nil
}
func (m *MemStore) PutTermsVersion(_ context.Context, v TermsVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terms[v.ID] = clone(v)
	return nil
}
func (m *MemStore) ListTermsVersions(_ context.Context, helperID string) ([]TermsVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TermsVersion
	for _, v := range m.terms {
		if v.HelperID == helperID {
			out = append(out, clone(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Terms.EffectiveFrom < out[j].Terms.EffectiveFrom })
	return out, nil
}
func (m *MemStore) PutBill(_ context.Context, b BillRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.bills[b.ID]; ok && old.Status == BillApproved {
		return ErrAlreadyApproved
	}
	m.bills[b.ID] = clone(b)
	return nil
}
func (m *MemStore) GetBill(_ context.Context, id string) (BillRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bills[id]
	if !ok {
		return BillRecord{}, ErrNotFound
	}
	return clone(b), nil
}
func (m *MemStore) ListBills(_ context.Context, helperID string) ([]BillRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BillRecord
	for _, b := range m.bills {
		if helperID == "" || b.HelperID == helperID {
			out = append(out, clone(b))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Period != out[j].Period {
			return out[i].Period > out[j].Period
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}
func (m *MemStore) ApproveBill(_ context.Context, id, expectedCheckHash string, a Approval) (BillRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bills[id]
	if !ok {
		return BillRecord{}, ErrNotFound
	}
	if b.Status == BillApproved {
		return BillRecord{}, ErrAlreadyApproved
	}
	if b.CheckHash != expectedCheckHash {
		return BillRecord{}, ErrTermsChanged
	}
	b.Status = BillApproved
	b.Approval = &a
	b.UpdatedAt = a.At
	m.bills[id] = clone(b)
	return clone(b), nil
}
func (m *MemStore) PutProposal(_ context.Context, p Proposal) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.proposals[p.ID]; ok {
		return clone(old), nil
	}
	m.proposals[p.ID] = clone(p)
	return clone(p), nil
}
func (m *MemStore) GetProposal(_ context.Context, id string) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return Proposal{}, ErrNotFound
	}
	return clone(p), nil
}
func (m *MemStore) ListProposals(_ context.Context, status string) ([]Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Proposal
	for _, p := range m.proposals {
		if status == "" || p.Status == status {
			out = append(out, clone(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (m *MemStore) CloseProposal(_ context.Context, id, status, resultID, at string) (Proposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return Proposal{}, ErrNotFound
	}
	if p.Status != "open" {
		return Proposal{}, ErrProposalClosed
	}
	p.Status, p.ResultID, p.ClosedAt = status, resultID, at
	m.proposals[id] = clone(p)
	return clone(p), nil
}
func (m *MemStore) PutTurn(_ context.Context, t Turn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.turns[t.ID]; ok {
		return nil // idempotent create
	}
	m.turns[t.ID] = clone(t)
	return nil
}
func (m *MemStore) GetTurn(_ context.Context, id string) (Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.turns[id]
	if !ok {
		return Turn{}, ErrNotFound
	}
	return clone(t), nil
}
func (m *MemStore) ListTurns(_ context.Context, sessionID string) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Turn
	for _, t := range m.turns {
		if t.SessionID == sessionID {
			out = append(out, clone(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (m *MemStore) ClaimTurn(_ context.Context, id, at string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.turns[id]
	if !ok {
		return false, ErrNotFound
	}
	if t.Status != TurnQueued {
		return false, nil
	}
	t.Status, t.UpdatedAt = TurnRunning, at
	m.turns[id] = t
	return true, nil
}
func (m *MemStore) UpdateTurn(_ context.Context, t Turn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.turns[t.ID]; !ok {
		return ErrNotFound
	}
	m.turns[t.ID] = clone(t)
	return nil
}
func (m *MemStore) AppendAudit(_ context.Context, e AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, e)
	return nil
}
func (m *MemStore) ListAudit(_ context.Context) ([]AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.audit))
	copy(out, m.audit)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (m *MemStore) Reset(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.helpers, m.sources, m.terms = map[string]Helper{}, map[string]Source{}, map[string]TermsVersion{}
	m.bills, m.proposals, m.turns = map[string]BillRecord{}, map[string]Proposal{}, map[string]Turn{}
	m.audit = nil
	return nil
}

func (m *MemStore) OpenTurns(_ context.Context) ([]Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Turn
	for _, t := range m.turns {
		if t.Status == TurnQueued || t.Status == TurnRunning {
			out = append(out, clone(t))
		}
	}
	return out, nil
}

// SummariseSessions groups turns by session, newest conversation first.
func SummariseSessions(turns []Turn) []SessionSummary {
	sort.Slice(turns, func(i, j int) bool { return turns[i].CreatedAt < turns[j].CreatedAt })
	by := map[string]*SessionSummary{}
	var order []string
	for _, t := range turns {
		ss := by[t.SessionID]
		if ss == nil {
			ss = &SessionSummary{ID: t.SessionID, Title: sessionTitle(t), FirstAt: t.CreatedAt}
			by[t.SessionID] = ss
			order = append(order, t.SessionID)
		}
		ss.Turns++
		ss.LastAt = max(ss.LastAt, t.CreatedAt, t.UpdatedAt)
	}
	out := make([]SessionSummary, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastAt > out[j].LastAt })
	return out
}

func sessionTitle(t Turn) string {
	txt := strings.TrimSpace(strings.Join(strings.Fields(t.UserText), " "))
	if txt == "" {
		if len(t.SourceIDs) > 0 {
			return "(attachment)"
		}
		return "(empty)"
	}
	if r := []rune(txt); len(r) > 60 {
		return string(r[:57]) + "…"
	}
	return txt
}

func (m *MemStore) ListSessions(_ context.Context) ([]SessionSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []Turn
	for _, t := range m.turns {
		all = append(all, t)
	}
	return SummariseSessions(all), nil
}

func (m *MemStore) DeleteSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, t := range m.turns {
		if t.SessionID == sessionID {
			delete(m.turns, id)
		}
	}
	return nil
}
