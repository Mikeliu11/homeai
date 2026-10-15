package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"helperpay/pricing"
	"helperpay/records"
)

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
func arr(items map[string]any, desc string) map[string]any {
	return map[string]any{"type": "array", "items": items, "description": desc}
}

var ruleSchema = obj(map[string]any{
	"id": str("short stable id, e.g. r1"), "kind": str("hourly|multiplier|adder|flat|per_unit|minimum|fee|reimbursement|per_period|rounding"),
	"service": str("stable key bills map to: hourly, date_night, regular_visit, deep_clean, half_hour_walk ..."), "label": str("the helper's own words"),
	"rateCents": num(""), "amountCents": num(""), "factor": num(""), "perHourCents": num(""), "unit": str(""), "from": str("HH:MM"), "until": str("HH:MM"),
	"when":  obj(map[string]any{"days": arr(str("mon..sun"), ""), "from": str("HH:MM"), "to": str("HH:MM"), "dates": str("us_federal_holidays"), "exactDates": arr(str("YYYY-MM-DD"), "")}),
	"hours": num(""), "per": str("booking|extra_child|..."), "percentOfBooking": num(""),
	"trigger":  obj(map[string]any{"sameDay": map[string]any{"type": "boolean"}, "cancelledWithinHours": num(""), "holiday": map[string]any{"type": "boolean"}}),
	"capCents": num(""), "receiptRequired": map[string]any{"type": "boolean"}, "period": str("week|month"), "minutes": num(""),
}, "kind")

var helperSchema = obj(map[string]any{"name": str(""), "role": str("babysitter|cleaner|dog walker|..."), "payMethod": str("Venmo|Zelle|cash|..."),
	"usualDays": arr(str("mon..sun"), "days they usually come"), "notes": str("")}, "name", "role")

var lineSchema = obj(map[string]any{
	"date": str("YYYY-MM-DD; omit for aggregate lines"), "service": str("service key from the terms, or a new key for an unagreed charge"),
	"start": str("HH:MM"), "end": str("HH:MM, 24:00 for midnight"), "qty": num("for per-unit lines"), "claimedCents": num("what the helper billed for this line"),
	"note": str("the original wording"), "flags": obj(map[string]any{"same_day": map[string]any{"type": "boolean"}, "cancelled": map[string]any{"type": "boolean"}, "receipt": map[string]any{"type": "boolean"}}),
	"noticeHours": num("for cancellations: hours of notice given"),
	"extras":      obj(map[string]any{"extra_child": num("")}), "bookingCents": num("base amount for percentage fees"),
}, "claimedCents")

// Tools is the list offered to the model. None of them commit anything.
var Tools = []ToolSpec{
	{Name: "get_helpers", Description: "List helpers with their current confirmed terms.", Schema: obj(map[string]any{})},
	{Name: "get_terms", Description: "All confirmed terms snapshots for a helper, with rules and effective dates.", Schema: obj(map[string]any{"helperId": str("")}, "helperId")},
	{Name: "get_bills", Description: "Bills on record (optionally one helper) with status and check summary.", Schema: obj(map[string]any{"helperId": str("")})},
	{Name: "get_source", Description: "Re-read an uploaded source by id (text). Images were already shown to you.", Schema: obj(map[string]any{"sourceId": str("")}, "sourceId")},
	{Name: "list_pending", Description: "Open proposals awaiting Jordan's confirmation.", Schema: obj(map[string]any{})},
	{Name: "calendar_facts", Description: "Weekdays, federal holidays and the helper's usual days in a YYYY-MM period.", Schema: obj(map[string]any{"period": str("YYYY-MM"), "helperId": str("")}, "period")},
	{Name: "propose_terms", Description: "Propose a full terms snapshot for a helper (new or existing) in force from effectiveFrom. Jordan must confirm it.",
		Schema: obj(map[string]any{"helperId": str("existing helper id, or omit and give newHelper"), "newHelper": helperSchema,
			"effectiveFrom": str("YYYY-MM-DD, or empty for Jordan's undated notes"), "rules": arr(ruleSchema, ""), "defaultPerVisit": str("service for unlabelled per-visit lines"),
			"sourceIds": arr(str(""), "sources this comes from"), "summary": str("one or two plain sentences of the terms"),
			"conflicts": arr(str(""), "disagreements between sources and how you resolved them"), "assumptions": arr(str(""), "readings you made (dates, ambiguities)"),
			"diff": arr(str(""), "for amendments: what changed vs the current terms")}, "rules", "summary", "sourceIds")},
	{Name: "propose_bill", Description: "Propose a bill's line items for one helper and one period. The engine checks it; Jordan confirms and approves.",
		Schema: obj(map[string]any{"helperId": str(""), "newHelper": helperSchema, "period": str("YYYY-MM"), "lines": arr(lineSchema, ""),
			"claimedTotalCents": num("the total the helper wrote"), "sourceIds": arr(str(""), ""), "summary": str(""), "assumptions": arr(str(""), "")}, "period", "lines", "claimedTotalCents", "sourceIds")},
	{Name: "check_bill", Description: "Read a bill on record with the engine's current check (read-only).", Schema: obj(map[string]any{"billId": str("")}, "billId")},
	{Name: "remember", Description: "Store a non-money household fact for future conversations.", Schema: obj(map[string]any{"fact": str("")}, "fact")},
}

// execTool runs one tool call. idx makes propose_* idempotent per turn.
func (r *Runner) execTool(ctx context.Context, t records.Turn, idx int, tu ToolUse) (string, error) {
	key := fmt.Sprintf("%s:%d", t.ID, idx)
	in := tu.Input
	switch tu.Name {
	case "get_helpers":
		return r.helpersSummary(ctx), nil
	case "get_terms":
		id, _ := in["helperId"].(string)
		vs, err := r.Store.ListTermsVersions(ctx, id)
		if err != nil {
			return "", err
		}
		return toJSON(vs), nil
	case "get_bills":
		id, _ := in["helperId"].(string)
		bs, err := r.Store.ListBills(ctx, id)
		if err != nil {
			return "", err
		}
		type bv struct {
			ID, HelperID, Period, Status, Headline string
			AgreedTotalCents, ClaimedTotalCents    pricing.Cents
		}
		var out []bv
		for _, b := range bs {
			v := bv{ID: b.ID, HelperID: b.HelperID, Period: b.Period, Status: string(b.Status), ClaimedTotalCents: b.Bill.ClaimedTotalCents}
			if b.Check != nil {
				v.Headline, v.AgreedTotalCents = b.Check.Headline, b.Check.AgreedTotalCents
			}
			out = append(out, v)
		}
		return toJSON(out), nil
	case "get_source":
		id, _ := in["sourceId"].(string)
		s, err := r.Store.GetSource(ctx, id)
		if err != nil {
			return "", err
		}
		if s.Text != "" {
			return s.Text, nil
		}
		return "(image source; it was attached to the conversation)", nil
	case "list_pending":
		ps, err := r.Store.ListProposals(ctx, "open")
		if err != nil {
			return "", err
		}
		return toJSON(ps), nil
	case "calendar_facts":
		period, _ := in["period"].(string)
		var usual []pricing.Day
		if id, _ := in["helperId"].(string); id != "" {
			if h, err := r.Store.GetHelper(ctx, id); err == nil {
				usual = h.UsualDays
			}
		}
		cf, err := pricing.Calendar(period, usual, r.Loc)
		if err != nil {
			return "", err
		}
		return toJSON(cf), nil
	case "propose_terms":
		var tp records.TermsProposal
		raw, _ := json.Marshal(in)
		if err := json.Unmarshal(raw, &tp); err != nil {
			return "", err
		}
		var tin struct {
			EffectiveFrom   string         `json:"effectiveFrom"`
			Rules           []pricing.Rule `json:"rules"`
			DefaultPerVisit string         `json:"defaultPerVisit"`
		}
		_ = json.Unmarshal(raw, &tin)
		tp.Terms = pricing.Terms{EffectiveFrom: tin.EffectiveFrom, Rules: tin.Rules, DefaultPerVisit: tin.DefaultPerVisit}
		if tp.NewHelper != nil && tp.NewHelper.Name == "" {
			tp.NewHelper = nil
		}
		p, err := r.Machine.ProposeTerms(ctx, key, "agent", tp)
		if err != nil {
			return "", err
		}
		r.addProposal(p.ID)
		return fmt.Sprintf("Proposal %s created (awaiting Jordan's confirmation). Summary: %s", p.ID, p.Summary), nil
	case "propose_bill":
		var bp records.BillProposal
		raw, _ := json.Marshal(in)
		if err := json.Unmarshal(raw, &bp); err != nil {
			return "", err
		}
		var bin struct {
			Period            string         `json:"period"`
			Lines             []pricing.Line `json:"lines"`
			ClaimedTotalCents pricing.Cents  `json:"claimedTotalCents"`
		}
		_ = json.Unmarshal(raw, &bin)
		bp.Bill = pricing.Bill{Period: bin.Period, Lines: bin.Lines, ClaimedTotalCents: bin.ClaimedTotalCents}
		if bp.NewHelper != nil && bp.NewHelper.Name == "" {
			bp.NewHelper = nil
		}
		p, err := r.Machine.ProposeBill(ctx, key, "agent", bp)
		if err != nil {
			return "", err
		}
		r.addProposal(p.ID)
		// Preview the check so the model can explain it (not stored; the real check runs on confirm).
		preview := r.previewCheck(ctx, bp)
		return fmt.Sprintf("Bill proposal %s created (awaiting Jordan's confirmation).\nENGINE RESULT (authoritative; quote it, do not recompute): %s", p.ID, preview), nil
	case "check_bill":
		id, _ := in["billId"].(string)
		b, err := r.Store.GetBill(ctx, id)
		if err != nil {
			return "", err
		}
		if b.Check == nil {
			return "This bill has no check yet (no confirmed terms).", nil
		}
		return toJSON(b.Check), nil
	case "remember":
		fact, _ := in["fact"].(string)
		if strings.TrimSpace(fact) == "" {
			return "", fmt.Errorf("fact is empty")
		}
		if err := r.Memory.Remember(ctx, key, fact); err != nil {
			return "", err
		}
		return "Remembered.", nil
	}
	return "", fmt.Errorf("unknown tool %s", tu.Name)
}

func (r *Runner) previewCheck(ctx context.Context, bp records.BillProposal) string {
	if bp.HelperID == "" {
		return "no confirmed terms for a new helper yet; the bill will be checked once terms are confirmed."
	}
	h, err := r.Store.GetHelper(ctx, bp.HelperID)
	if err != nil {
		return "helper not found"
	}
	vs, _ := r.Store.ListTermsVersions(ctx, h.ID)
	if len(vs) == 0 {
		return "no confirmed terms yet; the bill will be checked once terms are confirmed."
	}
	ts := make([]pricing.Terms, 0, len(vs))
	for _, v := range vs {
		ts = append(ts, v.Terms)
	}
	cal, _ := pricing.Calendar(bp.Bill.Period, h.UsualDays, r.Loc)
	cr := pricing.CheckBill(bp.Bill, ts, cal, nil, r.Loc)
	var sb strings.Builder
	sb.WriteString(cr.Headline + ". ")
	for i, l := range cr.Lines {
		if l.Status != pricing.StatusMatch {
			fmt.Fprintf(&sb, "Line %d (%s): %s — %s. ", i+1, firstNonEmpty(l.Line.Note, l.Line.Service), l.Status, l.Explanation)
		}
	}
	for _, f := range cr.Flags {
		sb.WriteString(f.Message + " ")
	}
	return sb.String()
}

func (r *Runner) helpersSummary(ctx context.Context) string {
	hs, _ := r.Store.ListHelpers(ctx)
	if len(hs) == 0 {
		return ""
	}
	var sb strings.Builder
	today := r.Now().In(r.Loc).Format("2006-01-02")
	for _, h := range hs {
		fmt.Fprintf(&sb, "- %s (id %s), %s, paid by %s", h.Name, h.ID, h.Role, firstNonEmpty(h.PayMethod, "unknown method"))
		vs, _ := r.Store.ListTermsVersions(ctx, h.ID)
		ts := make([]pricing.Terms, 0, len(vs))
		for _, v := range vs {
			ts = append(ts, v.Terms)
		}
		if cur, ok := pricing.TermsInForce(ts, today); ok {
			var rules []string
			for _, rl := range cur.Rules {
				rules = append(rules, ruleText(rl))
			}
			fmt.Fprintf(&sb, "; terms since %s: %s", firstNonEmpty(cur.EffectiveFrom, "(undated)"), strings.Join(rules, ", "))
		} else {
			sb.WriteString("; no confirmed terms")
		}
		if len(vs) > 1 {
			fmt.Fprintf(&sb, " (%d snapshots)", len(vs))
		}
		sb.WriteString("\n")
		bills, _ := r.Store.ListBills(ctx, h.ID)
		for _, b := range bills {
			fmt.Fprintf(&sb, "    bill %s (id %s): %s", b.Period, b.ID, b.Status)
			if b.Check != nil {
				fmt.Fprintf(&sb, " — %s", b.Check.Headline)
			}
			if b.Approval != nil {
				fmt.Fprintf(&sb, " — approved %s via %s", b.Approval.AmountCents, b.Approval.Method)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func ruleText(r pricing.Rule) string {
	switch r.Kind {
	case pricing.KindHourly:
		if r.When != nil && !r.When.IsEmpty() {
			return fmt.Sprintf("%s/hr %s [%s]", r.RateCents, r.Label, r.Service)
		}
		return fmt.Sprintf("%s/hr [%s]", r.RateCents, r.Service)
	case pricing.KindMultiplier:
		return fmt.Sprintf("×%v %s", r.Factor, r.Label)
	case pricing.KindFlat:
		return fmt.Sprintf("%s flat until %s [%s]", r.AmountCents, r.Until, r.Service)
	case pricing.KindPerUnit, pricing.KindPerPeriod, pricing.KindFee:
		return fmt.Sprintf("%s per %s [%s]", r.AmountCents, firstNonEmpty(r.Unit, r.Period, r.Label), r.Service)
	}
	return fmt.Sprintf("%s [%s]", r.Kind, firstNonEmpty(r.Service, r.Label))
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
