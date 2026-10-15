package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"helperpay/pricing"
	"helperpay/records"
)

type noInvoke struct{ calls []string }

func (n *noInvoke) Invoke(_ context.Context, id string) error {
	n.calls = append(n.calls, id)
	return nil
}

type fakeUploads struct{}

func (fakeUploads) PresignPut(_ context.Context, key, ct string) (string, error) {
	return "http://blob/" + key, nil
}
func (fakeUploads) Wipe(_ context.Context) error { return nil }
func (fakeUploads) PresignGet(_ context.Context, key string) (string, error) {
	return "http://blob/" + key, nil
}

func newServer() (*Server, *noInvoke) {
	st := records.NewMemStore()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	m := &records.Machine{Store: st, Loc: pricing.NY(), Now: func() time.Time { return now }}
	inv := &noInvoke{}
	return &Server{Store: st, Machine: m, Invoker: inv, Uploads: fakeUploads{}, Now: func() time.Time { return now }, StaleAfter: 5 * time.Minute}, inv
}

func do(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTurnIdempotentAndInvoked(t *testing.T) {
	s, inv := newServer()
	h := s.Handler()
	code, out := do(t, h, "POST", "/turns", map[string]any{"sessionId": "s1", "text": "hi", "clientKey": "k1"})
	if code != 202 || out["id"] != "k1" {
		t.Fatalf("%d %v", code, out)
	}
	code, _ = do(t, h, "POST", "/turns", map[string]any{"sessionId": "s1", "text": "hi", "clientKey": "k1"})
	if code != 200 || len(inv.calls) != 1 {
		t.Errorf("retry should not create or invoke again: %d %v", code, inv.calls)
	}
	code, _ = do(t, h, "POST", "/turns", map[string]any{"sessionId": "s1"})
	if code != 400 {
		t.Errorf("empty turn: %d", code)
	}
}

func TestConfirmAndApproveFlow(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	ctx := context.Background()
	tp := records.TermsProposal{NewHelper: &records.Helper{Name: "Lupe", Role: "cleaner", PayMethod: "Zelle", UsualDays: []pricing.Day{"tue"}},
		Terms: pricing.Terms{EffectiveFrom: "2026-01-14", DefaultPerVisit: "regular_visit", Rules: []pricing.Rule{
			{ID: "l1", Kind: pricing.KindPerUnit, Service: "regular_visit", Unit: "visit", AmountCents: 14000}}}, Summary: "Lupe $140/visit"}
	p, _ := s.Machine.ProposeTerms(ctx, "t:0", "agent", tp)

	code, out := do(t, h, "POST", "/proposals/"+p.ID+"/confirm", map[string]any{"hash": "nope"})
	if code != 409 || out["code"] != "hash_mismatch" {
		t.Fatalf("wrong hash: %d %v", code, out)
	}
	code, out = do(t, h, "POST", "/proposals/"+p.ID+"/confirm", map[string]any{"hash": p.ContentHash})
	if code != 200 || out["kind"] != "terms" {
		t.Fatalf("confirm: %d %v", code, out)
	}
	helperID := out["termsVersion"].(map[string]any)["helperId"].(string)

	bp := records.BillProposal{HelperID: helperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 30000, Lines: []pricing.Line{
		{Date: "2026-09-01", ClaimedCents: 15000}, {Date: "2026-09-08", ClaimedCents: 15000}}}}
	pb, _ := s.Machine.ProposeBill(ctx, "t:1", "agent", bp)
	code, out = do(t, h, "POST", "/proposals/"+pb.ID+"/confirm", map[string]any{"hash": pb.ContentHash})
	if code != 200 {
		t.Fatalf("confirm bill: %d %v", code, out)
	}
	billID := out["bill"].(map[string]any)["id"].(string)
	code, out = do(t, h, "GET", "/bills/"+billID, nil)
	if code != 200 {
		t.Fatal(code)
	}
	hash := out["hash"].(string)
	check := out["bill"].(map[string]any)["check"].(map[string]any)
	if check["headline"] != "Pay $280, not $300 — 2 problems" {
		t.Errorf("headline %v", check["headline"])
	}
	code, out = do(t, h, "POST", "/bills/"+billID+"/approve", map[string]any{"hash": hash, "method": "Zelle", "amountCents": 10000})
	if code != 422 || out["code"] != "note_required" {
		t.Fatalf("different amount without a note: %d %v", code, out)
	}
	code, out = do(t, h, "POST", "/bills/"+billID+"/approve", map[string]any{"hash": hash, "method": "Zelle"})
	if code != 200 {
		t.Fatalf("approve: %d %v", code, out)
	}
	if out["bill"].(map[string]any)["status"] != "approved" {
		t.Errorf("status %v", out["bill"].(map[string]any)["status"])
	}
	if msg, _ := out["message"].(string); msg == "" {
		t.Errorf("want a drafted message to the helper")
	}
	code, out = do(t, h, "POST", "/bills/"+billID+"/approve", map[string]any{"hash": hash, "method": "Zelle"})
	if code != 409 || out["code"] != "already_approved" {
		t.Errorf("second approve: %d %v", code, out)
	}
	code, _ = do(t, h, "POST", "/bills/"+billID+"/hold", map[string]any{"note": "x"})
	if code != 409 {
		t.Errorf("hold approved: %d", code)
	}
	code, out = do(t, h, "GET", "/helpers", nil)
	if code != 200 {
		t.Errorf("helpers: %d %v", code, out)
	}
}

func TestUploadsAndSources(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	code, out := do(t, h, "POST", "/uploads", map[string]any{"kind": "image", "contentType": "image/heic", "filename": "x.heic"})
	if code != 400 {
		t.Errorf("heic should be rejected: %d %v", code, out)
	}
	code, out = do(t, h, "POST", "/uploads", map[string]any{"kind": "image", "contentType": "image/png", "filename": "02-priya rates.png"})
	if code != 201 || out["putUrl"] == "" {
		t.Fatalf("png upload: %d %v", code, out)
	}
	id := out["source"].(map[string]any)["id"].(string)
	code, out = do(t, h, "GET", "/sources/"+id, nil)
	if code != 200 || out["getUrl"] == "" {
		t.Errorf("get source: %d %v", code, out)
	}
	code, _ = do(t, h, "POST", "/uploads", map[string]any{"kind": "text", "text": "hello"})
	if code != 201 {
		t.Errorf("text upload: %d", code)
	}
}

func TestStaleTurn(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	_ = s.Store.PutTurn(context.Background(), records.Turn{ID: "old", SessionID: "s", Status: records.TurnRunning, UpdatedAt: "2026-10-09T11:00:00Z", CreatedAt: "2026-10-09T11:00:00Z"})
	_, out := do(t, h, "GET", "/turns/old", nil)
	if out["turn"].(map[string]any)["status"] != "failed" {
		t.Errorf("stale running turn should read as failed: %v", out)
	}
}

func TestResetWipesEverythingAndLeavesOneAuditEvent(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	_ = s.Store.PutHelper(context.Background(), records.Helper{ID: "h1", Name: "Priya"})
	code, out := do(t, h, "POST", "/reset", map[string]any{"confirm": "nope"})
	if code != 400 || out["code"] != "confirm_required" {
		t.Fatalf("want 400 confirm_required, got %d %v", code, out)
	}
	if hs, _ := s.Store.ListHelpers(context.Background()); len(hs) != 1 {
		t.Fatalf("helper should survive a refused reset")
	}
	code, out = do(t, h, "POST", "/reset", map[string]any{"confirm": "RESET"})
	if code != 200 || out["ok"] != true {
		t.Fatalf("want 200 ok, got %d %v", code, out)
	}
	if hs, _ := s.Store.ListHelpers(context.Background()); len(hs) != 0 {
		t.Fatalf("helpers should be gone, got %d", len(hs))
	}
	a, _ := s.Store.ListAudit(context.Background())
	if len(a) != 1 || a[0].Action != "household.reset" {
		t.Fatalf("want exactly one household.reset audit event, got %+v", a)
	}
}

func TestResetRefusedWhileTurnRuns(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	_ = s.Store.PutTurn(context.Background(), records.Turn{ID: "t1", SessionID: "s", Status: records.TurnRunning})
	code, out := do(t, h, "POST", "/reset", map[string]any{"confirm": "RESET"})
	if code != 409 || out["code"] != "turn_in_progress" {
		t.Fatalf("want 409 turn_in_progress, got %d %v", code, out)
	}
}

type failingReset struct{ records.Store }

func (failingReset) Reset(context.Context) error { return errors.New("dynamo down") }

func TestResetReportsPartialFailureHonestly(t *testing.T) {
	s, _ := newServer()
	s.Store = failingReset{s.Store}
	h := s.Handler()
	code, out := do(t, h, "POST", "/reset", map[string]any{"confirm": "RESET"})
	if code != 207 || out["ok"] != false {
		t.Fatalf("want 207 ok=false, got %d %v", code, out)
	}
	if ps, _ := out["problems"].([]any); len(ps) != 1 || ps[0] != "records" {
		t.Fatalf("want problems [records], got %v", out["problems"])
	}
	a, _ := s.Store.ListAudit(context.Background())
	if len(a) != 1 || !strings.Contains(a[0].Detail, "could not delete all of: records") {
		t.Fatalf("audit should say what failed, got %+v", a)
	}
}

func TestSessionsListAndDelete(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	ctx := context.Background()
	_ = s.Store.PutTurn(ctx, records.Turn{ID: "t1", SessionID: "s1", UserText: "Text from Priya about rates", Status: records.TurnDone, CreatedAt: "2026-10-01T10:00:00Z", UpdatedAt: "2026-10-01T10:00:30Z", ProposalIDs: []string{"p1"}})
	_ = s.Store.PutTurn(ctx, records.Turn{ID: "t2", SessionID: "s2", UserText: "", SourceIDs: []string{"src"}, Status: records.TurnDone, CreatedAt: "2026-10-02T10:00:00Z", UpdatedAt: "2026-10-02T10:00:30Z"})
	_, _ = s.Store.PutProposal(ctx, records.Proposal{ID: "p1", Kind: "terms", Status: "open"})
	code, _ := do(t, h, "GET", "/sessions", nil)
	if code != 200 {
		t.Fatalf("list: %d", code)
	}
	ss, _ := s.Store.ListSessions(ctx)
	if len(ss) != 2 || ss[0].ID != "s2" || ss[0].Title != "(attachment)" || ss[1].Title != "Text from Priya about rates" {
		t.Fatalf("unexpected summaries %+v", ss)
	}
	code, out := do(t, h, "POST", "/sessions/s1/delete", nil)
	if code != 200 || out["rejectedCards"] != float64(1) {
		t.Fatalf("delete: %d %v", code, out)
	}
	if p, _ := s.Store.GetProposal(ctx, "p1"); p.Status != "rejected" {
		t.Fatalf("open card from the deleted conversation should be rejected, got %s", p.Status)
	}
	if ts, _ := s.Store.ListTurns(ctx, "s1"); len(ts) != 0 {
		t.Fatalf("turns should be gone")
	}
	code, _ = do(t, h, "POST", "/sessions/s1/delete", nil)
	if code != 404 {
		t.Fatalf("second delete should 404, got %d", code)
	}
	_ = s.Store.PutTurn(ctx, records.Turn{ID: "t3", SessionID: "s3", Status: records.TurnRunning, CreatedAt: "2026-10-03T10:00:00Z"})
	code, _ = do(t, h, "POST", "/sessions/s3/delete", nil)
	if code != 409 {
		t.Fatalf("running conversation should refuse delete, got %d", code)
	}
}

func TestResetIgnoresDeadTurn(t *testing.T) {
	s, _ := newServer()
	h := s.Handler()
	stale := s.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	_ = s.Store.PutTurn(context.Background(), records.Turn{ID: "dead", SessionID: "s", Status: records.TurnRunning, CreatedAt: stale, UpdatedAt: stale})
	code, out := do(t, h, "POST", "/reset", map[string]any{"confirm": "RESET"})
	if code != 200 {
		t.Fatalf("a turn silent for 10 minutes must not block the reset: %d %v", code, out)
	}
}
