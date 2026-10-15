// Package api is the HTTP surface behind Cognito. Every handler that commits
// terms or money lives here, bound to a UI button, and goes through
// records.Machine with the content hash the UI rendered.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"helperpay/agent"
	"helperpay/pricing"
	"helperpay/records"
)

// Invoker starts the agent on a turn (async Lambda invoke in prod, goroutine locally).
type Invoker interface {
	Invoke(ctx context.Context, turnID string) error
}

// Uploads issues presigned URLs for screenshots.
type Uploads interface {
	PresignPut(ctx context.Context, key, contentType string) (string, error)
	PresignGet(ctx context.Context, key string) (string, error)
	// Wipe deletes every upload (household reset).
	Wipe(ctx context.Context) error
}

type Server struct {
	Store   records.Store
	Machine *records.Machine
	Invoker Invoker
	Uploads Uploads
	Memory  agent.MemoryClient
	Now     func() time.Time
	// StaleAfter: a running turn with no update for this long is reported failed.
	StaleAfter time.Duration
	// AllowOrigin for CORS headers on every response.
	AllowOrigin string
}

type ctxKey int

const userKey ctxKey = 1

// WithUser puts the authenticated username on the context.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey, user)
}

func user(r *http.Request) string {
	if u, ok := r.Context().Value(userKey).(string); ok && u != "" {
		return u
	}
	return "jordan"
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /helpers", s.listHelpers)
	mux.HandleFunc("GET /helpers/{id}", s.getHelper)
	mux.HandleFunc("GET /proposals", s.listProposals)
	mux.HandleFunc("GET /proposals/{id}", s.getProposal)
	mux.HandleFunc("POST /proposals/{id}/confirm", s.confirmProposal)
	mux.HandleFunc("POST /proposals/{id}/reject", s.rejectProposal)
	mux.HandleFunc("GET /bills", s.listBills)
	mux.HandleFunc("GET /bills/{id}", s.getBill)
	mux.HandleFunc("POST /bills/{id}/approve", s.approveBill)
	mux.HandleFunc("POST /bills/{id}/hold", s.holdBill)
	mux.HandleFunc("POST /bills/{id}/recheck", s.recheckBill)
	mux.HandleFunc("POST /turns", s.createTurn)
	mux.HandleFunc("GET /turns", s.listTurns)
	mux.HandleFunc("GET /turns/{id}", s.getTurn)
	mux.HandleFunc("POST /uploads", s.createUpload)
	mux.HandleFunc("GET /sources/{id}", s.getSource)
	mux.HandleFunc("GET /memory", s.getMemory)
	mux.HandleFunc("GET /audit", s.getAudit)
	mux.HandleFunc("POST /reset", s.resetHousehold)
	mux.HandleFunc("GET /sessions", s.listSessions)
	mux.HandleFunc("POST /memory/clear", s.clearMemory)
	mux.HandleFunc("POST /sessions/{id}/delete", s.deleteSession)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { s.json(w, 200, map[string]string{"ok": "true"}) })
	return s.cors(mux)
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.AllowOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	code := 500
	switch {
	case errors.Is(err, records.ErrNotFound):
		code = 404
	case errors.Is(err, records.ErrHashMismatch), errors.Is(err, records.ErrTermsChanged), errors.Is(err, records.ErrAlreadyApproved), errors.Is(err, records.ErrProposalClosed):
		code = 409
	case errors.Is(err, records.ErrPendingUnresolved), errors.Is(err, records.ErrAmountRequired), errors.Is(err, records.ErrNoteRequired):
		code = 422
	case errors.Is(err, records.ErrInvalid):
		code = 400
	}
	if code == 500 {
		log.Printf("api error: %v", err)
		if !strings.Contains(err.Error(), "needs") {
			s.json(w, code, map[string]string{"error": "something went wrong; try again"})
			return
		}
		code = 400
	}
	s.json(w, code, map[string]string{"error": err.Error(), "code": codeName(err)})
}

func codeName(err error) string {
	switch {
	case errors.Is(err, records.ErrHashMismatch):
		return "hash_mismatch"
	case errors.Is(err, records.ErrTermsChanged):
		return "terms_changed"
	case errors.Is(err, records.ErrAlreadyApproved):
		return "already_approved"
	case errors.Is(err, records.ErrProposalClosed):
		return "proposal_closed"
	case errors.Is(err, records.ErrPendingUnresolved):
		return "pending_unresolved"
	case errors.Is(err, records.ErrAmountRequired):
		return "amount_required"
	case errors.Is(err, records.ErrNoteRequired):
		return "note_required"
	case errors.Is(err, records.ErrInvalid):
		return "invalid"
	}
	return "error"
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20)).Decode(v)
}

// ---- helpers ----

type helperView struct {
	records.Helper
	CurrentTerms *records.TermsVersion `json:"currentTerms,omitempty"`
	TermsCount   int                   `json:"termsCount"`
	OpenBills    int                   `json:"openBills"`
	Pending      int                   `json:"pendingProposals"`
}

func (s *Server) helperView(ctx context.Context, h records.Helper, today string) helperView {
	v := helperView{Helper: h}
	versions, _ := s.Store.ListTermsVersions(ctx, h.ID)
	v.TermsCount = len(versions)
	ts := make([]pricing.Terms, 0, len(versions))
	for _, tv := range versions {
		ts = append(ts, tv.Terms)
	}
	if cur, ok := pricing.TermsInForce(ts, today); ok {
		for i := range versions {
			if versions[i].Terms.EffectiveFrom == cur.EffectiveFrom {
				v.CurrentTerms = &versions[i]
			}
		}
	}
	bills, _ := s.Store.ListBills(ctx, h.ID)
	for _, b := range bills {
		if b.Status != records.BillApproved {
			v.OpenBills++
		}
	}
	props, _ := s.Store.ListProposals(ctx, "open")
	for _, p := range props {
		if proposalHelperID(p) == h.ID {
			v.Pending++
		}
	}
	return v
}

func proposalHelperID(p records.Proposal) string {
	var x struct {
		HelperID string `json:"helperId"`
	}
	_ = json.Unmarshal(p.Payload, &x)
	return x.HelperID
}

func (s *Server) today() string { return s.Now().In(pricing.NY()).Format("2006-01-02") }

func (s *Server) listHelpers(w http.ResponseWriter, r *http.Request) {
	hs, err := s.Store.ListHelpers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []helperView{}
	for _, h := range hs {
		out = append(out, s.helperView(r.Context(), h, s.today()))
	}
	s.json(w, 200, out)
}

func (s *Server) getHelper(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h, err := s.Store.GetHelper(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	versions, _ := s.Store.ListTermsVersions(ctx, h.ID)
	bills, _ := s.Store.ListBills(ctx, h.ID)
	props, _ := s.Store.ListProposals(ctx, "open")
	mine := []records.Proposal{}
	for _, p := range props {
		if proposalHelperID(p) == h.ID {
			mine = append(mine, p)
		}
	}
	srcIDs := map[string]bool{}
	for _, v := range versions {
		for _, id := range v.SourceIDs {
			srcIDs[id] = true
		}
	}
	sources := []records.Source{}
	for id := range srcIDs {
		if src, err := s.Store.GetSource(ctx, id); err == nil {
			sources = append(sources, src)
		}
	}
	audit, _ := s.Store.ListAudit(ctx)
	mineAudit := []records.AuditEvent{}
	for _, a := range audit {
		if a.EntityID == h.ID || strings.Contains(a.Detail, h.Name) {
			mineAudit = append(mineAudit, a)
		}
	}
	if versions == nil {
		versions = []records.TermsVersion{}
	}
	if bills == nil {
		bills = []records.BillRecord{}
	}
	s.json(w, 200, map[string]any{"helper": s.helperView(ctx, h, s.today()), "versions": versions, "bills": bills, "proposals": mine, "sources": sources, "audit": mineAudit})
}

// ---- proposals ----

func (s *Server) listProposals(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListProposals(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if ps == nil {
		ps = []records.Proposal{}
	}
	s.json(w, 200, ps)
}

func (s *Server) getProposal(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProposal(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 200, p)
}

type confirmReq struct {
	Hash  string          `json:"hash"`
	Edits json.RawMessage `json:"edits,omitempty"` // full edited payload (user edit → new proposal)
}

func (s *Server) confirmProposal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req confirmReq
	if err := decode(r, &req); err != nil {
		s.fail(w, fmt.Errorf("bad request: needs hash"))
		return
	}
	id := r.PathValue("id")
	p, err := s.Store.GetProposal(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if p.ContentHash != req.Hash {
		s.fail(w, records.ErrHashMismatch)
		return
	}
	if len(req.Edits) > 0 {
		var np records.Proposal
		switch p.Kind {
		case "terms":
			var tp records.TermsProposal
			if err := json.Unmarshal(req.Edits, &tp); err != nil {
				s.fail(w, fmt.Errorf("bad request: edits needs a terms proposal"))
				return
			}
			np, err = s.Machine.EditTermsProposal(ctx, id, tp, user(r))
		case "bill":
			var bp records.BillProposal
			if err := json.Unmarshal(req.Edits, &bp); err != nil {
				s.fail(w, fmt.Errorf("bad request: edits needs a bill proposal"))
				return
			}
			np, err = s.Machine.EditBillProposal(ctx, id, bp, user(r))
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		p = np
	}
	switch p.Kind {
	case "terms":
		v, err := s.Machine.ConfirmTerms(ctx, p.ID, p.ContentHash, user(r))
		if err != nil {
			s.fail(w, err)
			return
		}
		s.json(w, 200, map[string]any{"kind": "terms", "termsVersion": v})
	case "bill":
		b, err := s.Machine.ConfirmBill(ctx, p.ID, p.ContentHash, user(r))
		if err != nil {
			s.fail(w, err)
			return
		}
		s.json(w, 200, map[string]any{"kind": "bill", "bill": b})
	default:
		s.fail(w, fmt.Errorf("unknown proposal kind"))
	}
}

func (s *Server) rejectProposal(w http.ResponseWriter, r *http.Request) {
	if err := s.Machine.Reject(r.Context(), r.PathValue("id"), user(r)); err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 200, map[string]string{"status": "rejected"})
}

// ---- bills ----

func (s *Server) listBills(w http.ResponseWriter, r *http.Request) {
	bs, err := s.Store.ListBills(r.Context(), r.URL.Query().Get("helperId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if bs == nil {
		bs = []records.BillRecord{}
	}
	s.json(w, 200, bs)
}

type billView struct {
	Bill    records.BillRecord `json:"bill"`
	Helper  records.Helper     `json:"helper"`
	Sources []records.Source   `json:"sources"`
	Hash    string             `json:"hash"` // echo back on approve
	Message string             `json:"message"`
}

func (s *Server) billView(ctx context.Context, b records.BillRecord) billView {
	h, _ := s.Store.GetHelper(ctx, b.HelperID)
	v := billView{Bill: b, Helper: h, Hash: b.ContentHashForConfirm(), Sources: []records.Source{}}
	for _, id := range b.SourceIDs {
		if src, err := s.Store.GetSource(ctx, id); err == nil {
			v.Sources = append(v.Sources, src)
		}
	}
	v.Message = helperMessage(b, h)
	return v
}

func (s *Server) getBill(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.GetBill(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 200, s.billView(r.Context(), b))
}

type approveReq struct {
	Hash            string                `json:"hash"`
	AmountCents     pricing.Cents         `json:"amountCents"`
	Method          string                `json:"method"`
	Note            string                `json:"note"`
	IncludedPending []int                 `json:"includedPending"`
	ExcludedPending []int                 `json:"excludedPending"`
	PendingAmounts  map[int]pricing.Cents `json:"pendingAmounts,omitempty"`
}

func (s *Server) approveBill(w http.ResponseWriter, r *http.Request) {
	var req approveReq
	if err := decode(r, &req); err != nil {
		s.fail(w, fmt.Errorf("bad request: needs hash"))
		return
	}
	b, err := s.Machine.Approve(r.Context(), r.PathValue("id"), req.Hash, records.Approval{AmountCents: req.AmountCents, Method: req.Method, Note: req.Note,
		IncludedPending: req.IncludedPending, ExcludedPending: req.ExcludedPending, PendingAmounts: req.PendingAmounts, By: user(r)})
	if err != nil {
		if errors.Is(err, records.ErrTermsChanged) {
			s.json(w, 409, map[string]any{"error": err.Error(), "code": "terms_changed", "bill": s.billView(r.Context(), b)})
			return
		}
		s.fail(w, err)
		return
	}
	s.json(w, 200, s.billView(r.Context(), b))
}

func (s *Server) holdBill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	_ = decode(r, &req)
	if err := s.Machine.Hold(r.Context(), r.PathValue("id"), req.Note, user(r)); err != nil {
		s.fail(w, err)
		return
	}
	b, _ := s.Store.GetBill(r.Context(), r.PathValue("id"))
	s.json(w, 200, s.billView(r.Context(), b))
}

func (s *Server) recheckBill(w http.ResponseWriter, r *http.Request) {
	b, err := s.Machine.Recheck(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 200, s.billView(r.Context(), b))
}

// ---- turns ----

type turnReq struct {
	SessionID string   `json:"sessionId"`
	Text      string   `json:"text"`
	SourceIDs []string `json:"sourceIds"`
	ClientKey string   `json:"clientKey"` // idempotency: a retried POST does not create a second turn
}

func (s *Server) createTurn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req turnReq
	if err := decode(r, &req); err != nil || req.SessionID == "" || (strings.TrimSpace(req.Text) == "" && len(req.SourceIDs) == 0) {
		s.fail(w, fmt.Errorf("bad request: needs sessionId and text or sourceIds"))
		return
	}
	id := req.ClientKey
	if id == "" {
		id = records.NewID()
	}
	if _, err := s.Store.GetTurn(ctx, id); err == nil {
		t, _ := s.Store.GetTurn(ctx, id)
		s.json(w, 200, t)
		return
	}
	now := s.Now().UTC().Format(time.RFC3339)
	t := records.Turn{ID: id, SessionID: req.SessionID, UserText: req.Text, SourceIDs: req.SourceIDs, Status: records.TurnQueued, Progress: "queued", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.PutTurn(ctx, t); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Invoker.Invoke(ctx, t.ID); err != nil {
		t.Status, t.Error, t.UpdatedAt = records.TurnFailed, "could not start the assistant: "+err.Error(), now
		_ = s.Store.UpdateTurn(ctx, t)
	}
	s.json(w, 202, t)
}

func (s *Server) staleCheck(t records.Turn) records.Turn {
	if t.Status == records.TurnRunning || t.Status == records.TurnQueued {
		if upd, err := time.Parse(time.RFC3339, t.UpdatedAt); err == nil && s.StaleAfter > 0 && s.Now().Sub(upd) > s.StaleAfter {
			t.Status = records.TurnFailed
			t.Error = "the assistant did not finish; try again"
		}
	}
	return t
}

// anyLive reports whether a turn is genuinely queued or running: a turn silent
// longer than StaleAfter is dead (the Lambda cannot outlive it) and must not
// block a reset or a conversation delete forever.
func (s *Server) anyLive(turns []records.Turn) bool {
	for _, t := range turns {
		t = s.staleCheck(t)
		if t.Status == records.TurnQueued || t.Status == records.TurnRunning {
			return true
		}
	}
	return false
}

func (s *Server) getTurn(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTurn(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	t = s.staleCheck(t)
	props := []records.Proposal{}
	for _, id := range t.ProposalIDs {
		if p, err := s.Store.GetProposal(r.Context(), id); err == nil {
			props = append(props, p)
		}
	}
	s.json(w, 200, map[string]any{"turn": t, "proposals": props})
}

func (s *Server) listTurns(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.ListTurns(r.Context(), r.URL.Query().Get("sessionId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := []records.Turn{}
	for _, t := range ts {
		out = append(out, s.staleCheck(t))
	}
	s.json(w, 200, out)
}

// ---- uploads / sources ----

type uploadReq struct {
	Kind        string `json:"kind"` // text | image
	ContentType string `json:"contentType,omitempty"`
	Filename    string `json:"filename,omitempty"`
	Text        string `json:"text,omitempty"`
	SenderLabel string `json:"senderLabel,omitempty"`
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req uploadReq
	if err := decode(r, &req); err != nil {
		s.fail(w, fmt.Errorf("bad request: needs kind"))
		return
	}
	src := records.Source{ID: records.NewID(), Kind: req.Kind, SenderLabel: req.SenderLabel, UploadedAt: s.Now().UTC().Format(time.RFC3339)}
	var putURL string
	switch req.Kind {
	case "text", "note":
		if strings.TrimSpace(req.Text) == "" {
			s.fail(w, fmt.Errorf("bad request: text needs text"))
			return
		}
		src.Text = req.Text
	case "image":
		ct := req.ContentType
		if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" && ct != "image/gif" {
			s.fail(w, fmt.Errorf("bad request: image needs png, jpeg, webp or gif (got %q)", ct))
			return
		}
		src.ContentType = ct
		src.S3Key = "uploads/" + src.ID + "-" + safeName(req.Filename)
		u, err := s.Uploads.PresignPut(ctx, src.S3Key, ct)
		if err != nil {
			s.fail(w, err)
			return
		}
		putURL = u
	default:
		s.fail(w, fmt.Errorf("bad request: kind needs text, note or image"))
		return
	}
	if err := s.Store.PutSource(ctx, src); err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 201, map[string]any{"source": src, "putUrl": putURL})
}

func safeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "upload"
	}
	return b.String()
}

func (s *Server) getSource(w http.ResponseWriter, r *http.Request) {
	src, err := s.Store.GetSource(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	var getURL string
	if src.S3Key != "" {
		getURL, _ = s.Uploads.PresignGet(r.Context(), src.S3Key)
	}
	s.json(w, 200, map[string]any{"source": src, "getUrl": getURL})
}

func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	if s.Memory == nil {
		s.json(w, 200, []agent.MemoryRecord{})
		return
	}
	recs, err := s.Memory.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.json(w, 200, householdFacts(recs))
}

func (s *Server) getAudit(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.ListAudit(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if a == nil {
		a = []records.AuditEvent{}
	}
	s.json(w, 200, a)
}

// helperMessage drafts what Jordan can send the helper after approval.
func helperMessage(b records.BillRecord, h records.Helper) string {
	if b.Check == nil {
		return ""
	}
	c := b.Check
	period := b.Period
	if t, err := time.Parse("2006-01", period); err == nil {
		period = t.Format("January")
	}
	amount := c.AgreedTotalCents
	if b.Approval != nil {
		amount = b.Approval.AmountCents
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Hi %s! Sending %s for %s", h.Name, amount, period)
	if h.PayMethod != "" {
		fmt.Fprintf(&sb, " by %s", h.PayMethod)
	}
	sb.WriteString(".")
	var notes []string
	paidAsBilled := b.Approval != nil && b.Approval.AmountCents >= c.ClaimedTotalCents
	for _, l := range c.Lines {
		if paidAsBilled {
			break // Jordan chose to pay what was billed; don't argue the amount in the note
		}
		switch l.Status {
		case pricing.StatusOver:
			notes = append(notes, fmt.Sprintf("%s: we agreed %s, the bill has %s", lineName(l.Line), l.ExpectedCents, l.Line.ClaimedCents))
		case pricing.StatusUnder:
			notes = append(notes, fmt.Sprintf("%s: the bill has %s but we agreed %s; I paid what you billed, tell me if you'd rather I top it up", lineName(l.Line), l.Line.ClaimedCents, l.ExpectedCents))
		}
	}
	if !c.ArithmeticOK && !paidAsBilled {
		notes = append(notes, fmt.Sprintf("the lines add up to %s rather than %s", c.LineSumCents, c.ClaimedTotalCents))
	}
	if b.Approval != nil {
		for _, i := range b.Approval.ExcludedPending {
			if i < len(c.Lines) {
				switch c.Lines[i].Status {
				case pricing.StatusUnagreed:
					notes = append(notes, fmt.Sprintf("I left out the %s (%s) since we had not agreed it; happy to talk about it", lineName(c.Lines[i].Line), c.Lines[i].Line.ClaimedCents))
				default:
					notes = append(notes, fmt.Sprintf("I held back the %s (%s) until we've talked it through", lineName(c.Lines[i].Line), c.Lines[i].Line.ClaimedCents))
				}
			}
		}
		for i, amt := range b.Approval.PendingAmounts {
			if i < len(c.Lines) && amt != c.Lines[i].Line.ClaimedCents {
				notes = append(notes, fmt.Sprintf("%s: billed %s, I paid %s, the way we agreed to read that rate", lineName(c.Lines[i].Line), c.Lines[i].Line.ClaimedCents, amt))
			}
		}
	}
	if len(notes) > 0 {
		sb.WriteString(" One thing on the amount: " + strings.Join(notes, "; ") + ".")
	}
	if b.Approval != nil && b.Approval.Note != "" && b.Approval.AmountCents != c.AgreedTotalCents {
		note := strings.TrimRight(strings.TrimSpace(b.Approval.Note), ".")
		if r := []rune(note); len(r) > 0 {
			note = strings.ToUpper(string(r[0])) + string(r[1:])
		}
		sb.WriteString(" " + note + ".")
	}
	sb.WriteString(" Thank you!")
	return sb.String()
}

func lineName(l pricing.Line) string {
	what := strings.ReplaceAll(l.Service, "_", " ")
	if l.Start != "" {
		what = strings.TrimSpace(what + " " + l.Start + "–" + l.End)
	}
	if l.Date != "" {
		if t, err := time.Parse("2006-01-02", l.Date); err == nil {
			return strings.TrimSpace(t.Format("Jan 2") + " " + what)
		}
		return strings.TrimSpace(l.Date + " " + what)
	}
	if l.Note != "" {
		return l.Note
	}
	return what
}

// householdFacts drops memory records that carry money (those belong in
// records), dedupes, and caps the list.
func householdFacts(recs []agent.MemoryRecord) []agent.MemoryRecord {
	seen := map[string]bool{}
	out := []agent.MemoryRecord{}
	for _, r := range recs {
		t := strings.TrimSpace(r.Text)
		key := strings.ToLower(t)
		if t == "" || seen[key] || strings.Contains(t, "$") {
			continue
		}
		seen[key] = true
		out = append(out, r)
		if len(out) == 25 {
			break
		}
	}
	return out
}

// resetHousehold wipes every record, upload and memory so the household starts
// over as if freshly deployed. It is a demo affordance behind a typed
// confirmation; approved bills are immutable to edits, not to a full reset.
// Refused while a turn is queued or running, because the agent would write
// proposals and memory after the wipe.
func (s *Server) resetHousehold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Confirm != "RESET" {
		s.json(w, 400, map[string]any{"error": "Type RESET to confirm.", "code": "confirm_required"})
		return
	}
	ctx := r.Context()
	if open, err := s.Store.OpenTurns(ctx); err != nil {
		s.fail(w, err)
		return
	} else if s.anyLive(open) {
		s.json(w, 409, map[string]any{"error": "The assistant is still working on a message. Wait for it to finish, then try again.", "code": "turn_in_progress"})
		return
	}
	var problems []string
	note := func(what string, err error) {
		if err != nil {
			log.Printf("reset %s: %v", what, err)
			problems = append(problems, what)
		}
	}
	note("records", s.Store.Reset(ctx))
	if s.Uploads != nil {
		note("uploads", s.Uploads.Wipe(ctx))
	}
	if s.Memory != nil {
		// Memory is the slow part (one call per event). Keep inside API Gateway's 29 s;
		// what is left is reported, and running Start over again finishes it.
		mctx, cancel := context.WithTimeout(ctx, 22*time.Second)
		note("memory", s.Memory.Forget(mctx))
		cancel()
	}
	detail := "Everything was deleted: helpers, terms, bills, approvals, conversations, uploads and memory."
	if len(problems) > 0 {
		detail = "Reset attempted; could not delete all of: " + strings.Join(problems, ", ") + "."
	}
	note("audit", s.Store.AppendAudit(ctx, records.AuditEvent{TS: s.Now().UTC().Format(time.RFC3339), Actor: user(r), Action: "household.reset", Entity: "household", EntityID: "jordan", Detail: detail}))
	if len(problems) > 0 {
		s.json(w, 207, map[string]any{"ok": false, "problems": problems})
		return
	}
	s.json(w, 200, map[string]any{"ok": true})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := s.Store.ListSessions(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if ss == nil {
		ss = []records.SessionSummary{}
	}
	s.json(w, 200, ss)
}

// deleteSession removes one conversation: its turns, its memory events and
// summary, and any cards from it still waiting for confirmation (rejected, so
// they stop showing under Helpers and Bills). Confirmed records are untouched.
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	turns, err := s.Store.ListTurns(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(turns) == 0 {
		s.json(w, 404, map[string]any{"error": "No such conversation.", "code": "not_found"})
		return
	}
	now := s.Now().UTC().Format(time.RFC3339)
	if s.anyLive(turns) {
		s.json(w, 409, map[string]any{"error": "The assistant is still working on a message in this conversation. Wait for it to finish, then try again.", "code": "turn_in_progress"})
		return
	}
	rejected := 0
	for _, t := range turns {
		for _, pid := range t.ProposalIDs {
			p, err := s.Store.GetProposal(ctx, pid)
			if err != nil || p.Status != "open" {
				continue
			}
			if _, err := s.Store.CloseProposal(ctx, pid, "rejected", "", now); err == nil {
				rejected++
			}
		}
	}
	if err := s.Store.DeleteSession(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	var problems []string
	if s.Memory != nil {
		if err := s.Memory.ForgetSession(ctx, id); err != nil {
			log.Printf("delete session %s memory: %v", id, err)
			problems = append(problems, "memory")
		}
	}
	_ = s.Store.AppendAudit(ctx, records.AuditEvent{TS: now, Actor: user(r), Action: "conversation.deleted", Entity: "session", EntityID: id,
		Detail: fmt.Sprintf("%d messages deleted; %d unconfirmed cards rejected.", len(turns), rejected)})
	s.json(w, 200, map[string]any{"ok": len(problems) == 0, "turns": len(turns), "rejectedCards": rejected, "problems": problems})
}

// clearMemory forgets everything the assistant remembers (facts and conversation
// events) without touching records. Jordan's way to drop a wrong recollection,
// and to catch facts the extraction job wrote after a reset.
func (s *Server) clearMemory(w http.ResponseWriter, r *http.Request) {
	if s.Memory == nil {
		s.json(w, 200, map[string]any{"ok": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 22*time.Second)
	defer cancel()
	if err := s.Memory.Forget(ctx); err != nil {
		log.Printf("clear memory: %v", err)
		s.json(w, 207, map[string]any{"ok": false, "problems": []string{"memory"}})
		return
	}
	_ = s.Store.AppendAudit(ctx, records.AuditEvent{TS: s.Now().UTC().Format(time.RFC3339), Actor: user(r), Action: "memory.cleared", Entity: "memory", EntityID: "jordan", Detail: "Everything the assistant remembered was cleared; records untouched."})
	s.json(w, 200, map[string]any{"ok": true})
}
