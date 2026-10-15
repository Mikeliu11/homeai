package records

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"helperpay/pricing"
)

// Machine holds the state transitions. Every transition that commits money
// or terms takes the content hash the user saw and writes an audit event.
type Machine struct {
	Store Store
	Now   func() time.Time
	Loc   *time.Location
}

func (m *Machine) now() string { return m.Now().UTC().Format(time.RFC3339) }

// ---- proposals (the agent's only write path) ----

// ProposeTerms records a terms proposal. key is the idempotency key.
func (m *Machine) ProposeTerms(ctx context.Context, key, by string, tp TermsProposal) (Proposal, error) {
	if tp.HelperID == "" && tp.NewHelper == nil {
		return Proposal{}, fmt.Errorf("a terms proposal needs a helper")
	}
	for i := range tp.Terms.Rules {
		if tp.Terms.Rules[i].ID == "" {
			tp.Terms.Rules[i].ID = fmt.Sprintf("r%d", i+1)
		}
	}
	if errs := pricing.ValidateTerms(tp.Terms); len(errs) > 0 {
		return Proposal{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	if tp.HelperID != "" {
		if _, err := m.Store.GetHelper(ctx, tp.HelperID); err != nil {
			return Proposal{}, fmt.Errorf("helper %s: %w", tp.HelperID, err)
		}
	}
	p, err := m.putProposal(ctx, key, "terms", by, tp.Summary, tp)
	if err == nil {
		m.supersede(ctx, p, func(other Proposal) bool {
			var o TermsProposal
			return other.Kind == "terms" && json.Unmarshal(other.Payload, &o) == nil && sameHelper(o.HelperID, o.NewHelper, tp.HelperID, tp.NewHelper) && o.Terms.EffectiveFrom == tp.Terms.EffectiveFrom
		})
	}
	return p, err
}

// ProposeBill records a bill proposal.
func (m *Machine) ProposeBill(ctx context.Context, key, by string, bp BillProposal) (Proposal, error) {
	if bp.HelperID == "" && bp.NewHelper == nil {
		return Proposal{}, fmt.Errorf("a bill proposal needs a helper")
	}
	if errs := pricing.ValidateBill(bp.Bill); len(errs) > 0 {
		return Proposal{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	if bp.HelperID != "" {
		h, err := m.Store.GetHelper(ctx, bp.HelperID)
		if err != nil {
			return Proposal{}, fmt.Errorf("helper %s: %w", bp.HelperID, err)
		}
		bills, _ := m.Store.ListBills(ctx, h.ID)
		for _, b := range bills {
			if b.Period != bp.Bill.Period {
				continue
			}
			if b.Status == BillApproved {
				return Proposal{}, fmt.Errorf("%s's %s bill is already approved and locked (%s); a second bill for that month cannot be added", h.Name, b.Period, b.Approval.AmountCents)
			}
			bp.Assumptions = append(bp.Assumptions, fmt.Sprintf("This replaces the open %s bill already on record for %s (%s billed). Confirming it discards those lines.", b.Period, h.Name, b.Bill.ClaimedTotalCents))
		}
	}
	p, err := m.putProposal(ctx, key, "bill", by, bp.Summary, bp)
	if err == nil {
		m.supersede(ctx, p, func(other Proposal) bool {
			var o BillProposal
			return other.Kind == "bill" && json.Unmarshal(other.Payload, &o) == nil && sameHelper(o.HelperID, o.NewHelper, bp.HelperID, bp.NewHelper) && o.Bill.Period == bp.Bill.Period
		})
	}
	return p, err
}

// supersede closes earlier open proposals that this one replaces (the agent
// corrected itself, or Jordan pasted the same thing twice), so only one card
// per helper and period/date stays actionable.
func (m *Machine) supersede(ctx context.Context, p Proposal, same func(Proposal) bool) {
	open, _ := m.Store.ListProposals(ctx, "open")
	for _, o := range open {
		if o.ID == p.ID || o.CreatedAt > p.CreatedAt || !same(o) {
			continue
		}
		if _, err := m.Store.CloseProposal(ctx, o.ID, "superseded", p.ID, m.now()); err == nil {
			_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: "system", Action: "proposal.superseded", Entity: "proposal", EntityID: o.ID, Detail: "replaced by " + p.ID})
		}
	}
}

func sameHelper(idA string, newA *Helper, idB string, newB *Helper) bool {
	if idA != "" || idB != "" {
		return idA == idB
	}
	return newA != nil && newB != nil && equalFold(newA.Name, newB.Name)
}

func (m *Machine) putProposal(ctx context.Context, key, kind, by, summary string, payload any) (Proposal, error) {
	raw, _ := json.Marshal(payload)
	p := Proposal{ID: key, Kind: kind, Payload: raw, ContentHash: ContentHash(json.RawMessage(raw)), ProposedBy: by,
		Status: "open", Summary: summary, CreatedAt: m.now()}
	return m.Store.PutProposal(ctx, p)
}

// EditTermsProposal replaces an open proposal with a user-authored one
// (amounts/dates edited in the UI). The original is closed as "edited" and
// points at its replacement, so the UI can show the new card in its place.
// Labels and the summary that quote a changed amount are updated too.
func (m *Machine) EditTermsProposal(ctx context.Context, id string, tp TermsProposal, by string) (Proposal, error) {
	orig, err := m.Store.GetProposal(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	if orig.Status != "open" {
		return Proposal{}, ErrProposalClosed
	}
	var old TermsProposal
	_ = json.Unmarshal(orig.Payload, &old)
	tp = refreshTermsText(old, tp)
	if errs := pricing.ValidateTerms(tp.Terms); len(errs) > 0 {
		return Proposal{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	key := "user:" + NewID()
	if _, err := m.Store.CloseProposal(ctx, id, "edited", key, m.now()); err != nil {
		return Proposal{}, err
	}
	np, err := m.ProposeTerms(ctx, key, "user", tp)
	if err != nil {
		return Proposal{}, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "proposal.edited", Entity: "proposal", EntityID: id, Detail: "replaced by " + np.ID})
	return np, nil
}

// EditBillProposal is the same for bills.
func (m *Machine) EditBillProposal(ctx context.Context, id string, bp BillProposal, by string) (Proposal, error) {
	orig, err := m.Store.GetProposal(ctx, id)
	if err != nil {
		return Proposal{}, err
	}
	if orig.Status != "open" {
		return Proposal{}, ErrProposalClosed
	}
	if errs := pricing.ValidateBill(bp.Bill); len(errs) > 0 {
		return Proposal{}, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	key := "user:" + NewID()
	if _, err := m.Store.CloseProposal(ctx, id, "edited", key, m.now()); err != nil {
		return Proposal{}, err
	}
	np, err := m.ProposeBill(ctx, key, "user", bp)
	if err != nil {
		return Proposal{}, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "proposal.edited", Entity: "proposal", EntityID: id, Detail: "replaced by " + np.ID})
	return np, nil
}

// refreshTermsText rewrites money mentions in rule labels and the summary when
// an edit changed the amount, so "$30/hr" does not survive a change to $32.
func refreshTermsText(old, edited TermsProposal) TermsProposal {
	byID := map[string]pricing.Rule{}
	for _, r := range old.Terms.Rules {
		byID[r.ID] = r
	}
	for i, r := range edited.Terms.Rules {
		o, ok := byID[r.ID]
		if !ok {
			continue
		}
		pairs := [][2]pricing.Cents{{o.RateCents, r.RateCents}, {o.AmountCents, r.AmountCents}, {o.PerHourCents, r.PerHourCents}}
		for _, pr := range pairs {
			if pr[0] != pr[1] && pr[0] != 0 {
				edited.Terms.Rules[i].Label = strings.ReplaceAll(r.Label, pr[0].String(), pr[1].String())
				edited.Summary = strings.ReplaceAll(edited.Summary, pr[0].String(), pr[1].String())
			}
		}
	}
	if edited.Summary != "" && !strings.Contains(edited.Summary, "(edited") {
		edited.Summary += " (edited by Jordan)"
	}
	return edited
}

// Reject closes a proposal without effect.
func (m *Machine) Reject(ctx context.Context, id, by string) error {
	if _, err := m.Store.CloseProposal(ctx, id, "rejected", "", m.now()); err != nil {
		return err
	}
	return m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "proposal.rejected", Entity: "proposal", EntityID: id})
}

// ---- confirmations (UI buttons only) ----

func (m *Machine) openProposal(ctx context.Context, id, hash, kind string) (Proposal, error) {
	p, err := m.Store.GetProposal(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != "open" {
		return p, ErrProposalClosed
	}
	if p.Kind != kind {
		return p, fmt.Errorf("proposal %s is a %s proposal", id, p.Kind)
	}
	if p.ContentHash != hash || ContentHash(json.RawMessage(p.Payload)) != hash {
		return p, ErrHashMismatch
	}
	return p, nil
}

func (m *Machine) ensureHelper(ctx context.Context, id string, nh *Helper, by string) (Helper, error) {
	if id != "" {
		return m.Store.GetHelper(ctx, id)
	}
	// match by name: a second "Lupe" is the same Lupe
	if nh != nil {
		hs, _ := m.Store.ListHelpers(ctx)
		for _, h := range hs {
			if equalFold(h.Name, nh.Name) {
				merged := h
				if merged.Role == "" {
					merged.Role = nh.Role
				}
				if merged.PayMethod == "" {
					merged.PayMethod = nh.PayMethod
				}
				if len(merged.UsualDays) == 0 {
					merged.UsualDays = nh.UsualDays
				}
				_ = m.Store.PutHelper(ctx, merged)
				return merged, nil
			}
		}
	}
	if nh == nil || nh.Name == "" {
		return Helper{}, fmt.Errorf("no helper given")
	}
	h := *nh
	h.ID = NewID()
	h.CreatedAt = m.now()
	if err := m.Store.PutHelper(ctx, h); err != nil {
		return h, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "helper.created", Entity: "helper", EntityID: h.ID, Detail: h.Name})
	return h, nil
}

// ConfirmTerms turns an open terms proposal into a confirmed snapshot, then
// re-checks every unapproved bill of that helper.
func (m *Machine) ConfirmTerms(ctx context.Context, proposalID, hash, by string) (TermsVersion, error) {
	p, err := m.openProposal(ctx, proposalID, hash, "terms")
	if err != nil {
		return TermsVersion{}, err
	}
	var tp TermsProposal
	if err := json.Unmarshal(p.Payload, &tp); err != nil {
		return TermsVersion{}, err
	}
	h, err := m.ensureHelper(ctx, tp.HelperID, tp.NewHelper, by)
	if err != nil {
		return TermsVersion{}, err
	}
	t := tp.Terms
	t.HelperID = h.ID
	t.ConfirmedAt = m.now()
	if t.Label == "" {
		if t.EffectiveFrom == "" {
			t.Label = h.Name + ", undated terms"
		} else {
			t.Label = h.Name + ", terms since " + t.EffectiveFrom
		}
	}
	v := TermsVersion{ID: NewID(), HelperID: h.ID, Terms: t, SourceIDs: tp.SourceIDs, Summary: tp.Summary, Assumptions: tp.Assumptions,
		ProposalID: p.ID, ConfirmedAt: m.now(), ConfirmedBy: by}
	v.ContentHash = ContentHash(v.Terms)
	if err := m.Store.PutTermsVersion(ctx, v); err != nil {
		return v, err
	}
	if _, err := m.Store.CloseProposal(ctx, p.ID, "confirmed", v.ID, m.now()); err != nil {
		return v, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "terms.confirmed", Entity: "terms", EntityID: v.ID, Hash: v.ContentHash, Detail: v.Terms.Label})
	// re-check open bills
	bills, _ := m.Store.ListBills(ctx, h.ID)
	for _, b := range bills {
		if b.Status == BillApproved {
			continue
		}
		_, _ = m.Recheck(ctx, b.ID)
	}
	return v, nil
}

// ConfirmBill turns an open bill proposal into the helper's bill for that
// period (creating or replacing the open one), then checks it.
func (m *Machine) ConfirmBill(ctx context.Context, proposalID, hash, by string) (BillRecord, error) {
	p, err := m.openProposal(ctx, proposalID, hash, "bill")
	if err != nil {
		return BillRecord{}, err
	}
	var bp BillProposal
	if err := json.Unmarshal(p.Payload, &bp); err != nil {
		return BillRecord{}, err
	}
	h, err := m.ensureHelper(ctx, bp.HelperID, bp.NewHelper, by)
	if err != nil {
		return BillRecord{}, err
	}
	bp.Bill.HelperID = h.ID
	rec := BillRecord{ID: NewID(), HelperID: h.ID, Period: bp.Bill.Period, CreatedAt: m.now()}
	existing, _ := m.Store.ListBills(ctx, h.ID)
	for _, b := range existing {
		if b.Period == bp.Bill.Period {
			if b.Status == BillApproved {
				return BillRecord{}, fmt.Errorf("%s %s: %w", h.Name, b.Period, ErrAlreadyApproved)
			}
			rec = b // link to the open bill; lines are replaced by this paste
			_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "bill.replaced", Entity: "bill", EntityID: b.ID, Detail: "re-pasted " + b.Period})
			break
		}
	}
	rec.Bill, rec.SourceIDs, rec.Assumptions, rec.ProposalID = bp.Bill, bp.SourceIDs, bp.Assumptions, p.ID
	rec.Approval, rec.HoldNote = nil, ""
	if err := m.check(ctx, &rec, h); err != nil {
		return rec, err
	}
	if err := m.Store.PutBill(ctx, rec); err != nil {
		return rec, err
	}
	if _, err := m.Store.CloseProposal(ctx, p.ID, "confirmed", rec.ID, m.now()); err != nil {
		return rec, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "bill.confirmed", Entity: "bill", EntityID: rec.ID, Hash: rec.CheckHash, Detail: h.Name + " " + rec.Period})
	return rec, nil
}

// check runs the engine against the confirmed terms and stores the result on the record.
func (m *Machine) check(ctx context.Context, rec *BillRecord, h Helper) error {
	versions, err := m.Store.ListTermsVersions(ctx, h.ID)
	if err != nil {
		return err
	}
	rec.UpdatedAt = m.now()
	if len(versions) == 0 {
		rec.Status, rec.Check, rec.CheckHash = BillAwaitingTerms, nil, ""
		return nil
	}
	ts := make([]pricing.Terms, 0, len(versions))
	for _, v := range versions {
		ts = append(ts, v.Terms)
	}
	cal, _ := pricing.Calendar(rec.Period, h.UsualDays, m.Loc)
	other := map[string]bool{}
	bills, _ := m.Store.ListBills(ctx, h.ID)
	for _, b := range bills {
		if b.ID == rec.ID {
			continue
		}
		for _, l := range b.Bill.Lines {
			if l.Date != "" {
				other[l.Date] = true
			}
		}
	}
	cr := pricing.CheckBill(rec.Bill, ts, cal, other, m.Loc)
	rec.Check = &cr
	rec.CheckHash = ContentHash(cr)
	if rec.Status != BillHeld {
		rec.Status = BillChecked
	}
	return nil
}

// Recheck re-runs the engine on an unapproved bill.
func (m *Machine) Recheck(ctx context.Context, billID string) (BillRecord, error) {
	rec, err := m.Store.GetBill(ctx, billID)
	if err != nil {
		return rec, err
	}
	if rec.Status == BillApproved {
		return rec, ErrAlreadyApproved
	}
	h, err := m.Store.GetHelper(ctx, rec.HelperID)
	if err != nil {
		return rec, err
	}
	if err := m.check(ctx, &rec, h); err != nil {
		return rec, err
	}
	return rec, m.Store.PutBill(ctx, rec)
}

// Approve freezes a bill with Jordan's decision. The hash is the bill as the
// UI rendered it; the check is recomputed first so a terms change since then
// is caught.
func (m *Machine) Approve(ctx context.Context, billID, hash string, a Approval) (BillRecord, error) {
	rec, err := m.Store.GetBill(ctx, billID)
	if err != nil {
		return rec, err
	}
	if rec.Status == BillApproved {
		return rec, ErrAlreadyApproved
	}
	if rec.ContentHashForConfirm() != hash {
		return rec, ErrHashMismatch
	}
	h, err := m.Store.GetHelper(ctx, rec.HelperID)
	if err != nil {
		return rec, err
	}
	before := rec.CheckHash
	if err := m.check(ctx, &rec, h); err != nil {
		return rec, err
	}
	if rec.CheckHash != before {
		_ = m.Store.PutBill(ctx, rec)
		return rec, ErrTermsChanged
	}
	if rec.Check == nil {
		return rec, fmt.Errorf("no confirmed terms for %s yet", h.Name)
	}
	// every pending decision must be included or excluded
	inc := map[int]bool{}
	for _, i := range a.IncludedPending {
		inc[i] = true
	}
	exc := map[int]bool{}
	for _, i := range a.ExcludedPending {
		exc[i] = true
	}
	amount := rec.Check.AgreedTotalCents
	needsTyped := false
	for _, d := range rec.Check.PendingDecisions {
		chosen, hasChosen := a.PendingAmounts[d.LineIndex]
		switch {
		case hasChosen:
			ok := false
			for _, rd := range d.Readings {
				if rd.ExpectedCents == chosen {
					ok = true
				}
			}
			if !ok {
				return rec, fmt.Errorf("%s is not one of the readings for line %d", chosen, d.LineIndex+1)
			}
			amount += chosen
		case inc[d.LineIndex]:
			amount += rec.Bill.Lines[d.LineIndex].ClaimedCents
			if d.Status == pricing.StatusUnpriceable {
				needsTyped = true
			}
		case exc[d.LineIndex]:
		default:
			return rec, ErrPendingUnresolved
		}
	}
	if needsTyped && (a.AmountCents == 0 || a.Note == "") {
		return rec, ErrAmountRequired
	}
	if a.AmountCents == 0 {
		a.AmountCents = amount
	} else if a.AmountCents != amount && strings.TrimSpace(a.Note) == "" {
		return rec, ErrNoteRequired
	}
	if a.Method == "" {
		a.Method = h.PayMethod
	}
	versions, _ := m.Store.ListTermsVersions(ctx, h.ID)
	for _, v := range versions {
		a.TermsVersionIDs = append(a.TermsVersionIDs, v.ID)
	}
	a.EngineVersion, a.CheckSnapshot, a.CheckHash, a.At = rec.Check.EngineVersion, *rec.Check, rec.CheckHash, m.now()
	if a.IncludedPending == nil {
		a.IncludedPending = []int{}
	}
	if a.ExcludedPending == nil {
		a.ExcludedPending = []int{}
	}
	out, err := m.Store.ApproveBill(ctx, rec.ID, rec.CheckHash, a)
	if err != nil {
		return rec, err
	}
	_ = m.Store.AppendAudit(ctx, AuditEvent{TS: a.At, Actor: a.By, Action: "bill.approved", Entity: "bill", EntityID: rec.ID, Hash: a.CheckHash,
		Detail: fmt.Sprintf("%s %s: %s via %s", h.Name, rec.Period, a.AmountCents, a.Method)})
	return out, nil
}

// Hold parks an unapproved bill with a note.
func (m *Machine) Hold(ctx context.Context, billID, note, by string) error {
	rec, err := m.Store.GetBill(ctx, billID)
	if err != nil {
		return err
	}
	if rec.Status == BillApproved {
		return ErrAlreadyApproved
	}
	rec.Status, rec.HoldNote, rec.UpdatedAt = BillHeld, note, m.now()
	if err := m.Store.PutBill(ctx, rec); err != nil {
		return err
	}
	return m.Store.AppendAudit(ctx, AuditEvent{TS: m.now(), Actor: by, Action: "bill.held", Entity: "bill", EntityID: rec.ID, Detail: note})
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
