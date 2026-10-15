package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"helperpay/pricing"
	"helperpay/records"
)

type stubLLM struct {
	calls []ConverseRequest
	resps []ConverseResponse
	err   error
}

func (s *stubLLM) Converse(_ context.Context, req ConverseRequest) (ConverseResponse, error) {
	s.calls = append(s.calls, req)
	if s.err != nil {
		return ConverseResponse{}, s.err
	}
	i := len(s.calls) - 1
	if i >= len(s.resps) {
		i = len(s.resps) - 1
	}
	return s.resps[i], nil
}

func newRunner(llm LLM) (*Runner, *records.MemStore) {
	st := records.NewMemStore()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m := &records.Machine{Store: st, Loc: pricing.NY(), Now: func() time.Time { return now }}
	r := &Runner{Deps: Deps{Store: st, Machine: m, Blobs: NewLocalBlobs(), Memory: NewLocalMemory(), LLM: llm, Loc: pricing.NY(), Now: func() time.Time { return now }, Actor: "jordan"}}
	return r, st
}

func queue(st *records.MemStore, id, text string, srcs ...string) {
	_ = st.PutTurn(context.Background(), records.Turn{ID: id, SessionID: "s1", UserText: text, SourceIDs: srcs, Status: records.TurnQueued, CreatedAt: "2026-10-09T12:00:00Z", UpdatedAt: "2026-10-09T12:00:00Z"})
}

func TestLoopRoundTripsReasoningAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	propose := map[string]any{"newHelper": map[string]any{"name": "Lupe", "role": "cleaner"}, "effectiveFrom": "2026-01-14",
		"rules": []any{map[string]any{"kind": "per_unit", "service": "regular_visit", "unit": "visit", "amountCents": 14000}}, "sourceIds": []any{}, "summary": "Lupe $140"}
	llm := &stubLLM{resps: []ConverseResponse{
		{StopReason: "tool_use", Message: Message{Role: "assistant", Content: []ContentBlock{
			{Reasoning: &ReasoningBlock{Text: "thinking", Signature: "sig123"}},
			{ToolUse: &ToolUse{ID: "t1", Name: "propose_terms", Input: propose}},
			{ToolUse: &ToolUse{ID: "t2", Name: "propose_terms", Input: propose}}, // a duplicate call in the same turn
		}}},
		{StopReason: "end_turn", Message: Message{Role: "assistant", Content: []ContentBlock{{Text: "Drafted Lupe's terms; please confirm."}}}},
	}}
	r, st := newRunner(llm)
	queue(st, "turn1", "here is lupe's thread: $140 each visit")
	if err := r.RunTurn(ctx, "turn1"); err != nil {
		t.Fatal(err)
	}
	// reasoning block went back unchanged in the second request
	second := llm.calls[1]
	found := false
	for _, m := range second.Messages {
		for _, c := range m.Content {
			if c.Reasoning != nil && c.Reasoning.Signature == "sig123" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("reasoning block was not passed back")
	}
	// tool results came back as a user message with both ids
	last := second.Messages[len(second.Messages)-1]
	if last.Role != "user" || len(last.Content) != 2 || last.Content[0].ToolResult.ToolUseID != "t1" {
		t.Errorf("tool results: %+v", last)
	}
	ps, _ := st.ListProposals(ctx, "open")
	if len(ps) != 1 {
		t.Errorf("two identical tool calls → two proposals, the earlier one superseded, one open: %d open", len(ps))
	}
	all, _ := st.ListProposals(ctx, "")
	if len(all) != 2 {
		t.Errorf("want two proposal records: %d", len(all))
	}
	tr, _ := st.GetTurn(ctx, "turn1")
	if tr.Status != records.TurnDone || tr.Reply == "" || len(tr.ProposalIDs) != 2 {
		t.Errorf("turn: %+v", tr)
	}
	// running the same turn again does nothing (claimed)
	if err := r.RunTurn(ctx, "turn1"); err != nil {
		t.Fatal(err)
	}
	if len(llm.calls) != 2 {
		t.Errorf("re-run should not call the model: %d", len(llm.calls))
	}
	// memory recorded both events
	ev, _ := r.Memory.RecentEvents(ctx, "s1", 10)
	if len(ev) != 2 || ev[1].Role != "assistant" {
		t.Errorf("events: %+v", ev)
	}
}

func TestLoopFailureMarksTurnFailed(t *testing.T) {
	ctx := context.Background()
	llm := &stubLLM{err: errors.New("ThrottlingException: slow down")}
	r, st := newRunner(llm)
	queue(st, "t2", "hello")
	if err := r.RunTurn(ctx, "t2"); err == nil {
		t.Fatal("want error")
	}
	tr, _ := st.GetTurn(ctx, "t2")
	if tr.Status != records.TurnFailed || !strings.Contains(tr.Error, "throttling") {
		t.Errorf("turn: %+v", tr)
	}
}

func TestScriptedStarterPack(t *testing.T) {
	ctx := context.Background()
	r, st := newRunner(ScriptedLLM{})
	// Order: Theo's rate text first (dated) then Jordan's note (undated) — out of order on purpose.
	_ = st.PutSource(ctx, records.Source{ID: "s5", Kind: "text", Text: "Theo (Thu Aug 27): Quick note, I'm updating my rates for September onward: $28 for a 30-min walk, $38 for an hour."})
	queue(st, "a", "", "s5")
	if err := r.RunTurn(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	ps, _ := st.ListProposals(ctx, "open")
	if len(ps) != 1 || ps[0].Kind != "terms" {
		t.Fatalf("proposals: %+v", ps)
	}
	v, err := r.Machine.ConfirmTerms(ctx, ps[0].ID, ps[0].ContentHash, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	// Theo's bill → proposal keyed to the existing helper, preview in the reply
	_ = st.PutSource(ctx, records.Source{ID: "s8", Kind: "text", Text: "Subject: September walks: Biscuit\n30-min walks: 18 x $28 = $504\n60-min walks: 2 x $38 = $76\nTotal: $580"})
	queue(st, "b", "", "s8")
	if err := r.RunTurn(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	tr, _ := st.GetTurn(ctx, "b")
	if !strings.Contains(tr.Reply, "Pay $580") {
		t.Errorf("reply should carry the engine preview: %q", tr.Reply)
	}
	p, _ := st.GetProposal(ctx, tr.ProposalIDs[0])
	if !strings.Contains(string(p.Payload), v.HelperID) {
		t.Errorf("bill proposal should reference the existing helper id")
	}
	// cropped-date fixture: a source with no date still yields a proposal with an assumption
	_ = st.PutSource(ctx, records.Source{ID: "s2", Kind: "text", Text: "Priya: I usually do $22/hr. After 10pm it's $28/hr, and Sundays are time and a half. date nights flat $100 until 11"})
	queue(st, "c", "", "s2")
	_ = r.RunTurn(ctx, "c")
	tr, _ = st.GetTurn(ctx, "c")
	p, _ = st.GetProposal(ctx, tr.ProposalIDs[0])
	if !strings.Contains(string(p.Payload), "assumptions") {
		t.Errorf("want assumptions on the card")
	}
}

func TestFailedTurnDoesNotBreakTheConversation(t *testing.T) {
	ctx := context.Background()
	llm := &stubLLM{err: errors.New("ThrottlingException: slow down")}
	r, st := newRunner(llm)
	queue(st, "turn1", "here is lupe's thread: $140 each visit")
	if err := r.RunTurn(ctx, "turn1"); err == nil {
		t.Fatal("first turn should fail")
	}
	// the next turn in the same session must send alternating roles
	llm.err = nil
	llm.resps = []ConverseResponse{{StopReason: "end_turn", Message: Message{Role: "assistant", Content: []ContentBlock{{Text: "Got it."}}}}}
	_ = st.PutTurn(ctx, records.Turn{ID: "turn2", SessionID: "s1", UserText: "sorry, again: $140 each visit", Status: records.TurnQueued, CreatedAt: "2026-10-09T12:01:00Z", UpdatedAt: "2026-10-09T12:01:00Z"})
	if err := r.RunTurn(ctx, "turn2"); err != nil {
		t.Fatal(err)
	}
	msgs := llm.calls[len(llm.calls)-1].Messages
	if len(msgs) == 0 || msgs[0].Role != "user" {
		t.Fatalf("history must start with a user message: %+v", msgs)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Fatalf("roles must alternate; message %d and %d are both %s", i-1, i, msgs[i].Role)
		}
	}
	if msgs[len(msgs)-1].Role != "user" || !strings.Contains(msgs[len(msgs)-1].Content[len(msgs[len(msgs)-1].Content)-1].Text, "again") {
		t.Fatalf("the new message must be the last user block: %+v", msgs[len(msgs)-1])
	}
}

func TestTruncatedToolCallIsNotRun(t *testing.T) {
	ctx := context.Background()
	propose := map[string]any{"newHelper": map[string]any{"name": "Lupe", "role": "cleaner"}, "effectiveFrom": "2026-01-14",
		"rules": []any{map[string]any{"kind": "per_unit", "service": "regular_visit", "unit": "visit", "amountCents": 14000}}, "sourceIds": []any{}, "summary": "Lupe $140"}
	llm := &stubLLM{resps: []ConverseResponse{{StopReason: "max_tokens", Message: Message{Role: "assistant", Content: []ContentBlock{
		{ToolUse: &ToolUse{ID: "t1", Name: "propose_terms", Input: propose}}}}}}}
	r, st := newRunner(llm)
	queue(st, "turn1", "lupe")
	_ = r.RunTurn(ctx, "turn1")
	if ps, _ := st.ListProposals(ctx, "open"); len(ps) != 0 {
		t.Fatalf("a cut-off tool call must not create a card, got %d", len(ps))
	}
}

func TestEmptyToolResultIsNeverSent(t *testing.T) {
	ctx := context.Background()
	llm := &stubLLM{resps: []ConverseResponse{
		{StopReason: "tool_use", Message: Message{Role: "assistant", Content: []ContentBlock{{ToolUse: &ToolUse{ID: "t1", Name: "get_helpers", Input: map[string]any{}}}}}},
		{StopReason: "end_turn", Message: Message{Role: "assistant", Content: []ContentBlock{{Text: "No helpers yet."}}}},
	}}
	r, st := newRunner(llm)
	queue(st, "turn1", "who do we have?")
	if err := r.RunTurn(ctx, "turn1"); err != nil {
		t.Fatal(err)
	}
	last := llm.calls[1].Messages[len(llm.calls[1].Messages)-1]
	if last.Content[0].ToolResult == nil || strings.TrimSpace(last.Content[0].ToolResult.Text) == "" {
		t.Fatalf("empty tool result sent: %+v", last)
	}
}
