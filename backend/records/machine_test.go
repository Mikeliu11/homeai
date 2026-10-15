package records

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"helperpay/pricing"
)

func newMachine() (*Machine, *MemStore) {
	st := NewMemStore()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, pricing.NY())
	m := &Machine{Store: st, Loc: pricing.NY(), Now: func() time.Time { now = now.Add(time.Second); return now }}
	return m, st
}

func lupeTermsP(from string, regular pricing.Cents) TermsProposal {
	return TermsProposal{
		NewHelper: &Helper{Name: "Lupe", Role: "cleaner", PayMethod: "Zelle", UsualDays: []pricing.Day{"tue"}},
		Terms: pricing.Terms{EffectiveFrom: from, DefaultPerVisit: "regular_visit", Rules: []pricing.Rule{
			{ID: "l1", Kind: pricing.KindPerUnit, Service: "regular_visit", Label: "regular visit", Unit: "visit", AmountCents: regular},
			{ID: "l2", Kind: pricing.KindPerUnit, Service: "deep_clean", Label: "deep clean", Unit: "visit", AmountCents: 22000},
		}},
		SourceIDs: []string{"src1"}, Summary: "Regular visit " + regular.String() + ", deep clean $220",
	}
}

func lupeBillP(helperID string) BillProposal {
	return BillProposal{HelperID: helperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 82000, Lines: []pricing.Line{
		{Date: "2026-09-01", ClaimedCents: 15000}, {Date: "2026-09-08", ClaimedCents: 15000}, {Date: "2026-09-15", ClaimedCents: 15000},
		{Date: "2026-09-22", Service: "deep_clean", ClaimedCents: 22000}, {Date: "2026-09-29", ClaimedCents: 15000}}},
		SourceIDs: []string{"src2"}, Summary: "September: 5 visits, $820"}
}

func TestConfirmTermsOutOfOrderAndHashes(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	// Jordan pastes the September change first, then the January thread.
	pSep, err := m.ProposeTerms(ctx, "turn1:0", "agent", lupeTermsP("2026-09-15", 15000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConfirmTerms(ctx, pSep.ID, "wrong-hash", "jordan"); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("want hash mismatch, got %v", err)
	}
	vSep, err := m.ConfirmTerms(ctx, pSep.ID, pSep.ContentHash, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	// second confirm of the same proposal is refused
	if _, err := m.ConfirmTerms(ctx, pSep.ID, pSep.ContentHash, "jordan"); !errors.Is(err, ErrProposalClosed) {
		t.Errorf("want proposal closed, got %v", err)
	}
	jan := lupeTermsP("2026-01-14", 14000)
	jan.NewHelper = nil
	jan.HelperID = vSep.HelperID
	pJan, _ := m.ProposeTerms(ctx, "turn2:0", "agent", jan)
	if _, err := m.ConfirmTerms(ctx, pJan.ID, pJan.ContentHash, "jordan"); err != nil {
		t.Fatal(err)
	}
	// bill: Sep 1 must price at $140 even though January was confirmed last
	pb, _ := m.ProposeBill(ctx, "turn3:0", "agent", lupeBillP(vSep.HelperID))
	bill, err := m.ConfirmBill(ctx, pb.ID, pb.ContentHash, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	if bill.Status != BillChecked || bill.Check == nil || bill.Check.AgreedTotalCents != 80000 {
		t.Fatalf("bill: %s %+v", bill.Status, bill.Check)
	}
	if bill.Check.Lines[0].ExpectedCents != 14000 {
		t.Errorf("Sep 1 expected $140, got %d", bill.Check.Lines[0].ExpectedCents)
	}
	audit, _ := m.Store.ListAudit(ctx)
	if len(audit) < 4 {
		t.Errorf("want audit events for helper, 2 terms, bill; got %d", len(audit))
	}
}

func TestProposalIdempotent(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	a, _ := m.ProposeTerms(ctx, "turnX:0", "agent", lupeTermsP("2026-01-14", 14000))
	b, _ := m.ProposeTerms(ctx, "turnX:0", "agent", lupeTermsP("2026-01-14", 14000))
	if a.ID != b.ID || a.CreatedAt != b.CreatedAt {
		t.Errorf("same key should return the same proposal")
	}
	ps, _ := m.Store.ListProposals(ctx, "open")
	if len(ps) != 1 {
		t.Errorf("want 1 open proposal, got %d", len(ps))
	}
}

func TestBillFlow(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	// Bill before any terms → awaiting_terms, then auto-checked when terms are confirmed
	pb, _ := m.ProposeBill(ctx, "t1:0", "agent", lupeBillP(""))
	pbPayload := lupeBillP("")
	pbPayload.NewHelper = &Helper{Name: "Lupe", Role: "cleaner"}
	pb, _ = m.ProposeBill(ctx, "t1:1", "agent", pbPayload)
	bill, err := m.ConfirmBill(ctx, pb.ID, pb.ContentHash, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	if bill.Status != BillAwaitingTerms {
		t.Fatalf("want awaiting_terms, got %s", bill.Status)
	}
	jan := lupeTermsP("2026-01-14", 14000)
	jan.NewHelper, jan.HelperID = nil, bill.HelperID
	pJan, _ := m.ProposeTerms(ctx, "t2:0", "agent", jan)
	if _, err := m.ConfirmTerms(ctx, pJan.ID, pJan.ContentHash, "jordan"); err != nil {
		t.Fatal(err)
	}
	bill, _ = m.Store.GetBill(ctx, bill.ID)
	if bill.Status != BillChecked || bill.Check == nil || bill.Check.AgreedTotalCents != 78000 { // 4×$140 + $220
		t.Fatalf("after terms: %s %+v", bill.Status, bill.Check)
	}
	// Re-pasting the same bill links to the existing open bill
	pb2, _ := m.ProposeBill(ctx, "t3:0", "agent", lupeBillP(bill.HelperID))
	bill2, err := m.ConfirmBill(ctx, pb2.ID, pb2.ContentHash, "jordan")
	if err != nil || bill2.ID != bill.ID {
		t.Fatalf("second September bill should link to the first: %v %s vs %s", err, bill2.ID, bill.ID)
	}
	bills, _ := m.Store.ListBills(ctx, bill.HelperID)
	if len(bills) != 1 {
		t.Fatalf("want exactly one bill, got %d", len(bills))
	}
	// Approve: terms changed since check → refused with new check
	sep := lupeTermsP("2026-09-15", 15000)
	sep.NewHelper, sep.HelperID = nil, bill.HelperID
	pSep, _ := m.ProposeTerms(ctx, "t4:0", "agent", sep)
	staleHash := bill2.ContentHashForConfirm()
	if _, err := m.ConfirmTerms(ctx, pSep.ID, pSep.ContentHash, "jordan"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Approve(ctx, bill.ID, staleHash, Approval{Method: "Zelle", By: "jordan"})
	if !errors.Is(err, ErrHashMismatch) && !errors.Is(err, ErrTermsChanged) {
		t.Fatalf("stale approve should be refused, got %v", err)
	}
	bill, _ = m.Store.GetBill(ctx, bill.ID)
	if bill.Check.AgreedTotalCents != 80000 {
		t.Fatalf("re-check after terms change: %d", bill.Check.AgreedTotalCents)
	}
	// Approve for real with the fresh hash and default amount (agreed total)
	approved, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Zelle", By: "jordan"})
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != BillApproved || approved.Approval.AmountCents != 80000 || approved.Approval.CheckHash == "" {
		t.Errorf("approval: %+v", approved.Approval)
	}
	// Immutable afterwards
	if _, err := m.Approve(ctx, bill.ID, approved.ContentHashForConfirm(), Approval{Method: "Zelle"}); !errors.Is(err, ErrAlreadyApproved) {
		t.Errorf("second approve: %v", err)
	}
	if _, err := m.Recheck(ctx, bill.ID); !errors.Is(err, ErrAlreadyApproved) {
		t.Errorf("recheck approved: %v", err)
	}
	if err := m.Hold(ctx, bill.ID, "x", "jordan"); !errors.Is(err, ErrAlreadyApproved) {
		t.Errorf("hold approved: %v", err)
	}
	// A new September bill for the same helper is refused: the period is approved
	if _, err := m.ProposeBill(ctx, "t5:0", "agent", lupeBillP(bill.HelperID)); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Errorf("new bill for approved period should be refused at proposal time: %v", err)
	}
	// re-paste of an open bill carries a visible note
	m3, _ := newMachine()
	pt3, _ := m3.ProposeTerms(ctx, "x:0", "agent", lupeTermsP("2026-01-14", 14000))
	v3, _ := m3.ConfirmTerms(ctx, pt3.ID, pt3.ContentHash, "jordan")
	pbA, _ := m3.ProposeBill(ctx, "x:1", "agent", lupeBillP(v3.HelperID))
	_, _ = m3.ConfirmBill(ctx, pbA.ID, pbA.ContentHash, "jordan")
	pbB, _ := m3.ProposeBill(ctx, "x:2", "agent", lupeBillP(v3.HelperID))
	if !strings.Contains(string(pbB.Payload), "replaces the open") {
		t.Errorf("second bill proposal should say it replaces the open one")
	}
}

func TestApprovePendingDecisions(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	priya := TermsProposal{NewHelper: &Helper{Name: "Priya", Role: "babysitter", PayMethod: "Venmo"},
		Terms: pricing.Terms{EffectiveFrom: "2026-06-14", Rules: []pricing.Rule{
			{ID: "p1", Kind: pricing.KindHourly, Service: "hourly", RateCents: 2200},
			{ID: "p2", Kind: pricing.KindHourly, Service: "hourly", Label: "after 10pm", RateCents: 2800, When: &pricing.Window{From: "22:00"}},
			{ID: "p3", Kind: pricing.KindMultiplier, Label: "Sundays ×1.5", Factor: 1.5, When: &pricing.Window{Days: []pricing.Day{"sun"}}},
			{ID: "p4", Kind: pricing.KindFlat, Service: "date_night", Label: "date night", AmountCents: 10000, Until: "23:00"}}},
		SourceIDs: []string{"s"}, Summary: "Priya rates"}
	pt, _ := m.ProposeTerms(ctx, "a:0", "agent", priya)
	v, _ := m.ConfirmTerms(ctx, pt.ID, pt.ContentHash, "jordan")
	bp := BillProposal{HelperID: v.HelperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 62600, Lines: []pricing.Line{
		{Date: "2026-09-04", Service: "date_night", Start: "18:00", End: "23:30", ClaimedCents: 11400},
		{Date: "2026-09-09", Service: "hourly", Start: "15:00", End: "19:00", ClaimedCents: 8800},
		{Date: "2026-09-12", Service: "hourly", Start: "19:00", End: "24:00", ClaimedCents: 12200},
		{Date: "2026-09-20", Service: "hourly", Start: "10:00", End: "14:00", ClaimedCents: 13200},
		{Date: "2026-09-20", Service: "last_minute_fee", ClaimedCents: 4000, Note: "last minute fee"},
		{Date: "2026-09-25", Service: "date_night", Start: "18:00", End: "23:00", ClaimedCents: 10000}}}, SourceIDs: []string{"s2"}}
	pb, _ := m.ProposeBill(ctx, "b:0", "agent", bp)
	bill, err := m.ConfirmBill(ctx, pb.ID, pb.ContentHash, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	if len(bill.Check.PendingDecisions) != 1 {
		t.Fatalf("pending: %+v", bill.Check.PendingDecisions)
	}
	// No decision on the $40 → refused
	if _, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan"}); !errors.Is(err, ErrPendingUnresolved) {
		t.Fatalf("want pending unresolved, got %v", err)
	}
	// Exclude it → pays $556
	a, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan", ExcludedPending: []int{4}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Approval.AmountCents != 55600 {
		t.Errorf("excluded fee: %d", a.Approval.AmountCents)
	}
}

func TestApproveIncludesPendingAndUnpriceableNeedsAmount(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	pt, _ := m.ProposeTerms(ctx, "a:0", "agent", lupeTermsP("2026-01-14", 14000))
	v, _ := m.ConfirmTerms(ctx, pt.ID, pt.ContentHash, "jordan")
	bp := BillProposal{HelperID: v.HelperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 29000, Lines: []pricing.Line{
		{Date: "2026-09-01", ClaimedCents: 14000},
		{Date: "2026-09-08", Service: "window_washing", ClaimedCents: 15000, Note: "windows"}}}}
	pb, _ := m.ProposeBill(ctx, "b:0", "agent", bp)
	bill, _ := m.ConfirmBill(ctx, pb.ID, pb.ContentHash, "jordan")
	a, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Zelle", By: "jordan", IncludedPending: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Approval.AmountCents != 29000 {
		t.Errorf("included unagreed line: %d", a.Approval.AmountCents)
	}

	// unpriceable line (hourly without times) → typed amount + note required
	m2, _ := newMachine()
	pt2, _ := m2.ProposeTerms(ctx, "a:0", "agent", TermsProposal{NewHelper: &Helper{Name: "P", Role: "sitter"},
		Terms: pricing.Terms{EffectiveFrom: "2026-01-01", Rules: []pricing.Rule{{ID: "h", Kind: pricing.KindHourly, Service: "hourly", RateCents: 2000}}}})
	v2, _ := m2.ConfirmTerms(ctx, pt2.ID, pt2.ContentHash, "jordan")
	pb2, _ := m2.ProposeBill(ctx, "b:0", "agent", BillProposal{HelperID: v2.HelperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 5000,
		Lines: []pricing.Line{{Date: "2026-09-02", Service: "hourly", ClaimedCents: 5000, Note: "Wednesday"}}}})
	bill2, _ := m2.ConfirmBill(ctx, pb2.ID, pb2.ContentHash, "jordan")
	if _, err := m2.Approve(ctx, bill2.ID, bill2.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan", IncludedPending: []int{0}}); !errors.Is(err, ErrAmountRequired) {
		t.Errorf("unpriceable without typed amount: %v", err)
	}
	a2, err := m2.Approve(ctx, bill2.ID, bill2.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan", IncludedPending: []int{0}, AmountCents: 5000, Note: "2.5h, confirmed by text"})
	if err != nil || a2.Approval.AmountCents != 5000 {
		t.Errorf("typed amount: %v %+v", err, a2.Approval)
	}
}

func TestUserEditProposal(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	p, _ := m.ProposeTerms(ctx, "a:0", "agent", lupeTermsP("2026-01-14", 14000))
	edited := lupeTermsP("2026-01-20", 14000)
	np, err := m.EditTermsProposal(ctx, p.ID, edited, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	if np.ProposedBy != "user" || np.ID == p.ID {
		t.Errorf("edit should create a new user proposal: %+v", np)
	}
	old, _ := m.Store.GetProposal(ctx, p.ID)
	if old.Status != "edited" || old.ResultID != np.ID {
		t.Errorf("original should be marked edited and point at the replacement, got %s %s", old.Status, old.ResultID)
	}
	// labels and summary follow an amount change
	p2, _ := m.ProposeTerms(ctx, "b:0", "agent", TermsProposal{NewHelper: &Helper{Name: "Dana", Role: "house manager"}, Summary: "Dana charges $30/hr.",
		Terms: pricing.Terms{EffectiveFrom: "2026-09-14", Rules: []pricing.Rule{{ID: "r1", Kind: pricing.KindHourly, Service: "hourly", Label: "$30/hr", RateCents: 3000}}}})
	var tp TermsProposal
	_ = json.Unmarshal(p2.Payload, &tp)
	tp.Terms.Rules[0].RateCents = 3200
	np2, err := m.EditTermsProposal(ctx, p2.ID, tp, "jordan")
	if err != nil {
		t.Fatal(err)
	}
	var out TermsProposal
	_ = json.Unmarshal(np2.Payload, &out)
	if out.Terms.Rules[0].Label != "$32/hr" || !strings.Contains(out.Summary, "$32/hr") {
		t.Errorf("label/summary not refreshed: %q %q", out.Terms.Rules[0].Label, out.Summary)
	}
}

func TestApproveChoosesReadingAndNeedsNoteWhenDifferent(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	priya := TermsProposal{NewHelper: &Helper{Name: "Priya", Role: "babysitter", PayMethod: "Venmo"},
		Terms: pricing.Terms{EffectiveFrom: "2026-06-14", Rules: []pricing.Rule{
			{ID: "p1", Kind: pricing.KindHourly, Service: "hourly", RateCents: 2200},
			{ID: "p2", Kind: pricing.KindHourly, Service: "hourly", Label: "after 10pm", RateCents: 2800, When: &pricing.Window{From: "22:00"}},
			{ID: "p4", Kind: pricing.KindFlat, Service: "date_night", Label: "date night", AmountCents: 10000, Until: "23:00"}}}, SourceIDs: []string{"s"}}
	pt, _ := m.ProposeTerms(ctx, "a:0", "agent", priya)
	v, _ := m.ConfirmTerms(ctx, pt.ID, pt.ContentHash, "jordan")
	// Friday 18–23 billed hourly $116: ambiguous (date night reading $100)
	pb, _ := m.ProposeBill(ctx, "b:0", "agent", BillProposal{HelperID: v.HelperID, Bill: pricing.Bill{Period: "2026-09", ClaimedTotalCents: 11600,
		Lines: []pricing.Line{{Date: "2026-09-11", Service: "hourly", Start: "18:00", End: "23:00", ClaimedCents: 11600}}}})
	bill, _ := m.ConfirmBill(ctx, pb.ID, pb.ContentHash, "jordan")
	if len(bill.Check.PendingDecisions) != 1 || bill.Check.PendingDecisions[0].Status != pricing.StatusAmbiguous {
		t.Fatalf("pending: %+v", bill.Check.PendingDecisions)
	}
	if _, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan", PendingAmounts: map[int]pricing.Cents{0: 9999}}); err == nil {
		t.Errorf("a made-up reading must be refused")
	}
	a, err := m.Approve(ctx, bill.ID, bill.ContentHashForConfirm(), Approval{Method: "Venmo", By: "jordan", PendingAmounts: map[int]pricing.Cents{0: 10000}})
	if err != nil || a.Approval.AmountCents != 10000 {
		t.Errorf("chosen reading: %v %+v", err, a.Approval)
	}
	// typed amount that differs from the computed one needs a note
	m2, _ := newMachine()
	pt2, _ := m2.ProposeTerms(ctx, "a:0", "agent", lupeTermsP("2026-01-14", 14000))
	v2, _ := m2.ConfirmTerms(ctx, pt2.ID, pt2.ContentHash, "jordan")
	pb2, _ := m2.ProposeBill(ctx, "b:0", "agent", lupeBillP(v2.HelperID))
	b2, _ := m2.ConfirmBill(ctx, pb2.ID, pb2.ContentHash, "jordan")
	if _, err := m2.Approve(ctx, b2.ID, b2.ContentHashForConfirm(), Approval{Method: "Zelle", By: "jordan", AmountCents: 70000}); !errors.Is(err, ErrNoteRequired) {
		t.Errorf("different amount without note: %v", err)
	}
	if _, err := m2.Approve(ctx, b2.ID, b2.ContentHashForConfirm(), Approval{Method: "Zelle", By: "jordan", AmountCents: 70000, Note: "she waived one visit"}); err != nil {
		t.Errorf("different amount with note: %v", err)
	}
}

func TestInvalidProposalsRefused(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	bad := lupeTermsP("Sept 15", 14000)
	if _, err := m.ProposeTerms(ctx, "a:0", "agent", bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad date: %v", err)
	}
	bad = lupeTermsP("2026-01-14", 14000)
	bad.Terms.Rules = append(bad.Terms.Rules, pricing.Rule{ID: "x", Kind: pricing.KindHourly, Service: "hourly", RateCents: 2200, When: &pricing.Window{From: "10pm"}})
	if _, err := m.ProposeTerms(ctx, "a:1", "agent", bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad clock: %v", err)
	}
	if _, err := m.ProposeBill(ctx, "b:0", "agent", BillProposal{NewHelper: &Helper{Name: "X", Role: "y"}, Bill: pricing.Bill{Period: "2026-9", Lines: []pricing.Line{{ClaimedCents: 1}}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad period: %v", err)
	}
}

func TestSupersedeEarlierProposal(t *testing.T) {
	ctx := context.Background()
	m, _ := newMachine()
	a, _ := m.ProposeTerms(ctx, "t:0", "agent", lupeTermsP("2026-01-14", 14000))
	b, _ := m.ProposeTerms(ctx, "t:1", "agent", lupeTermsP("2026-01-14", 14500)) // the agent corrected itself
	pa, _ := m.Store.GetProposal(ctx, a.ID)
	if pa.Status != "superseded" || pa.ResultID != b.ID {
		t.Errorf("first proposal should be superseded by the second: %+v", pa)
	}
	if _, err := m.ConfirmTerms(ctx, a.ID, a.ContentHash, "jordan"); !errors.Is(err, ErrProposalClosed) {
		t.Errorf("superseded proposal must not be confirmable: %v", err)
	}
	// a different effective date is a different proposal, not a replacement
	c, _ := m.ProposeTerms(ctx, "t:2", "agent", lupeTermsP("2026-09-15", 15000))
	pb, _ := m.Store.GetProposal(ctx, b.ID)
	if pb.Status != "open" || c.Status != "open" {
		t.Errorf("different dates should both stay open: %s %s", pb.Status, c.Status)
	}
}

func TestMemStoreResetClearsAll(t *testing.T) {
	st := NewMemStore()
	ctx := context.Background()
	_ = st.PutHelper(ctx, Helper{ID: "h1"})
	_ = st.PutBill(ctx, BillRecord{ID: "b1", HelperID: "h1"})
	_, _ = st.PutProposal(ctx, Proposal{ID: "p1", Status: "open"})
	_ = st.PutTurn(ctx, Turn{ID: "t1", SessionID: "s"})
	_ = st.AppendAudit(ctx, AuditEvent{Action: "x"})
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	hs, _ := st.ListHelpers(ctx)
	bs, _ := st.ListBills(ctx, "")
	ps, _ := st.ListProposals(ctx, "")
	ts, _ := st.ListTurns(ctx, "s")
	as, _ := st.ListAudit(ctx)
	if len(hs)+len(bs)+len(ps)+len(ts)+len(as) != 0 {
		t.Fatalf("not empty: %d %d %d %d %d", len(hs), len(bs), len(ps), len(ts), len(as))
	}
}
