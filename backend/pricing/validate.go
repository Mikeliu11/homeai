package pricing

import (
	"fmt"
	"time"
)

// priceable kinds price a bill line by its service; the others (multiplier,
// adder, minimum, rounding) only modify hourly pricing.
var priceable = map[RuleKind]bool{KindHourly: true, KindFlat: true, KindPerUnit: true, KindPerPeriod: true, KindFee: true, KindReimbursement: true}

var knownKinds = map[RuleKind]bool{KindHourly: true, KindMultiplier: true, KindAdder: true, KindFlat: true, KindPerUnit: true,
	KindMinimum: true, KindFee: true, KindReimbursement: true, KindPerPeriod: true, KindRounding: true}

// ValidateTerms returns every problem with a snapshot. Proposals that fail
// are refused at creation, so the model (or the UI editor) gets told and
// nothing malformed can reach the engine.
func ValidateTerms(t Terms) []string {
	var errs []string
	if t.EffectiveFrom != "" {
		if _, err := time.Parse("2006-01-02", t.EffectiveFrom); err != nil {
			errs = append(errs, fmt.Sprintf("effectiveFrom %q is not YYYY-MM-DD", t.EffectiveFrom))
		}
	}
	if t.EffectiveTo != "" {
		if _, err := time.Parse("2006-01-02", t.EffectiveTo); err != nil {
			errs = append(errs, fmt.Sprintf("effectiveTo %q is not YYYY-MM-DD", t.EffectiveTo))
		}
	}
	if len(t.Rules) == 0 {
		errs = append(errs, "at least one rule is required")
	}
	seen := map[string]bool{}
	svcKind := map[string]RuleKind{} // service → kind of the first priceable rule for it
	singles := map[RuleKind]int{}    // minimum and rounding: at most one each
	for i, r := range t.Rules {
		at := fmt.Sprintf("rule %d (%s)", i+1, firstNonEmpty(r.Label, r.Service, string(r.Kind)))
		if !knownKinds[r.Kind] {
			errs = append(errs, at+": unknown kind "+string(r.Kind))
			continue
		}
		if r.ID != "" {
			if seen[r.ID] {
				errs = append(errs, at+": duplicate id "+r.ID)
			}
			seen[r.ID] = true
		}
		if r.When != nil {
			if err := r.When.Validate(); err != nil {
				errs = append(errs, at+": when: "+err.Error())
			}
		}
		for _, c := range []Clock{r.From, r.Until} {
			if c != "" {
				if _, _, err := parseClock(c); err != nil {
					errs = append(errs, at+": "+err.Error())
				}
			}
		}
		if priceable[r.Kind] && r.Service != "" {
			// several hourly rules may share a service (a base rate and an evening rate); anything else would be priced by whichever came first
			if k, ok := svcKind[r.Service]; ok && (k != KindHourly || r.Kind != KindHourly) {
				errs = append(errs, fmt.Sprintf("%s: service %q already has a %s rule; each service needs one way to price it", at, r.Service, k))
			} else if !ok {
				svcKind[r.Service] = r.Kind
			}
		}
		if r.Kind == KindMinimum || r.Kind == KindRounding {
			if singles[r.Kind]++; singles[r.Kind] > 1 {
				errs = append(errs, fmt.Sprintf("%s: only one %s rule is allowed", at, r.Kind))
			}
		}
		switch r.Kind {
		case KindHourly:
			if r.RateCents <= 0 {
				errs = append(errs, at+": hourly needs rateCents > 0")
			}
			if r.Service == "" {
				errs = append(errs, at+": hourly needs a service key (e.g. hourly)")
			}
		case KindMultiplier:
			if r.Factor <= 0 {
				errs = append(errs, at+": multiplier needs factor > 0")
			}
			if r.When == nil || r.When.IsEmpty() {
				errs = append(errs, at+": multiplier needs a when (days, times or dates)")
			}
		case KindAdder:
			if r.PerHourCents <= 0 {
				errs = append(errs, at+": adder needs perHourCents > 0")
			}
			if r.Per == "" && (r.When == nil || r.When.IsEmpty()) {
				errs = append(errs, at+": adder needs per (what it counts, e.g. extra_child) or a when")
			}
		case KindFlat:
			if r.AmountCents <= 0 || r.Service == "" {
				errs = append(errs, at+": flat needs amountCents > 0 and a service key")
			}
		case KindPerUnit, KindPerPeriod:
			if r.AmountCents <= 0 || r.Service == "" {
				errs = append(errs, at+": "+string(r.Kind)+" needs amountCents > 0 and a service key")
			}
		case KindMinimum:
			if r.Hours <= 0 && r.AmountCents <= 0 {
				errs = append(errs, at+": minimum needs hours or amountCents")
			}
		case KindFee:
			if r.AmountCents <= 0 && r.PercentOfBooking <= 0 {
				errs = append(errs, at+": fee needs amountCents or percentOfBooking")
			}
			if r.Service == "" {
				errs = append(errs, at+": fee needs a service key")
			}
		case KindReimbursement:
			if r.Service == "" {
				errs = append(errs, at+": reimbursement needs a service key")
			}
		case KindRounding:
			if r.Minutes <= 0 {
				errs = append(errs, at+": rounding needs minutes > 0")
			}
		}
	}
	return errs
}

// ValidateBill checks a bill's shape before it becomes a proposal.
func ValidateBill(b Bill) []string {
	var errs []string
	if _, err := time.Parse("2006-01", b.Period); err != nil {
		errs = append(errs, fmt.Sprintf("period %q is not YYYY-MM", b.Period))
	}
	if len(b.Lines) == 0 {
		errs = append(errs, "at least one line is required")
	}
	for i, l := range b.Lines {
		at := fmt.Sprintf("line %d", i+1)
		if l.Date != "" {
			if _, err := time.Parse("2006-01-02", l.Date); err != nil {
				errs = append(errs, fmt.Sprintf("%s: date %q is not YYYY-MM-DD", at, l.Date))
			}
		}
		for _, c := range []Clock{l.Start, l.End} {
			if c != "" {
				if _, _, err := parseClock(c); err != nil {
					errs = append(errs, at+": "+err.Error())
				}
			}
		}
		if (l.Start == "") != (l.End == "") {
			errs = append(errs, at+": start and end must both be given")
		}
		if l.Qty < 0 {
			errs = append(errs, at+": negative quantity")
		}
		// a negative amount is a credit ("-$20 discount"): accepted, and the engine puts it to Jordan
	}
	return errs
}
