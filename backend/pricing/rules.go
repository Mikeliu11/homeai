package pricing

// RuleKind enumerates what the engine can price. One file per kind would be
// nice for a live change; the kinds are small enough to live in engine.go with
// a switch, and adding one is: a constant, a case, a test.
type RuleKind string

const (
	KindHourly        RuleKind = "hourly"        // RateCents per hour, optionally only inside When (e.g. after 22:00)
	KindMultiplier    RuleKind = "multiplier"    // Factor applied to hourly pricing inside When (e.g. Sundays ×1.5)
	KindAdder         RuleKind = "adder"         // PerHourCents added per unit of Line.Extras[Per] (e.g. +$3/hr per extra child)
	KindFlat          RuleKind = "flat"          // AmountCents covers [From, Until] on the day; time outside is priced hourly
	KindPerUnit       RuleKind = "per_unit"      // AmountCents × Line.Qty (visit, walk, mile)
	KindMinimum       RuleKind = "minimum"       // hourly booking is billed at least Hours (or AmountCents)
	KindFee           RuleKind = "fee"           // AmountCents when Trigger is met (same-day, cancellation, holiday)
	KindReimbursement RuleKind = "reimbursement" // pass-through expense, optionally capped
	KindPerPeriod     RuleKind = "per_period"    // AmountCents per Period (retainer)
	KindRounding      RuleKind = "rounding"      // hourly minutes round up to a multiple of Minutes
)

// Trigger is the condition for a fee.
type Trigger struct {
	SameDay              bool `json:"sameDay,omitempty"`
	CancelledWithinHours int  `json:"cancelledWithinHours,omitempty"`
	Holiday              bool `json:"holiday,omitempty"`
}

// Rule is one agreed term. Only the fields for its Kind are used.
type Rule struct {
	ID      string   `json:"id"`
	Kind    RuleKind `json:"kind"`
	Service string   `json:"service,omitempty"` // stable key the bill extractor maps lines to
	Label   string   `json:"label,omitempty"`   // human wording from the source ("date night")

	RateCents        Cents    `json:"rateCents,omitempty"`
	AmountCents      Cents    `json:"amountCents,omitempty"`
	Factor           float64  `json:"factor,omitempty"`
	PerHourCents     Cents    `json:"perHourCents,omitempty"`
	Unit             string   `json:"unit,omitempty"`
	From             Clock    `json:"from,omitempty"`
	Until            Clock    `json:"until,omitempty"`
	When             *Window  `json:"when,omitempty"`
	Hours            float64  `json:"hours,omitempty"`
	Per              string   `json:"per,omitempty"`
	PercentOfBooking float64  `json:"percentOfBooking,omitempty"`
	Trigger          *Trigger `json:"trigger,omitempty"`
	CapCents         Cents    `json:"capCents,omitempty"`
	ReceiptRequired  bool     `json:"receiptRequired,omitempty"`
	Period           string   `json:"period,omitempty"`
	Minutes          int      `json:"minutes,omitempty"`
}

// Terms is one confirmed snapshot of a helper's agreement, in force from EffectiveFrom.
type Terms struct {
	HelperID        string `json:"helperId"`
	EffectiveFrom   string `json:"effectiveFrom"` // YYYY-MM-DD; "" = undated (lowest precedence)
	EffectiveTo     string `json:"effectiveTo,omitempty"`
	Rules           []Rule `json:"rules"`
	DefaultPerVisit string `json:"defaultPerVisit,omitempty"` // service used for unlabelled per-unit lines
	Label           string `json:"label,omitempty"`           // e.g. "terms since 2026-09-15"
	ConfirmedAt     string `json:"confirmedAt,omitempty"`     // tie-break when two snapshots share an effectiveFrom: latest wins
}

// Line is one bill line as extracted. Either a time span (Start/End) or a Qty.
type Line struct {
	Date         string             `json:"date,omitempty"` // YYYY-MM-DD; "" = undated aggregate
	Service      string             `json:"service,omitempty"`
	Qty          float64            `json:"qty,omitempty"`
	Start        Clock              `json:"start,omitempty"`
	End          Clock              `json:"end,omitempty"`
	ClaimedCents Cents              `json:"claimedCents"`
	Note         string             `json:"note,omitempty"`         // original text
	Flags        map[string]bool    `json:"flags,omitempty"`        // same_day, cancelled, receipt
	Extras       map[string]float64 `json:"extras,omitempty"`       // extra_child: 1
	BookingCents Cents              `json:"bookingCents,omitempty"` // base for percent fees
	NoticeHours  float64            `json:"noticeHours,omitempty"`  // for cancellations: how much notice was given
}

// Status is the verdict on a line.
type Status string

const (
	StatusMatch       Status = "match"
	StatusOver        Status = "over"
	StatusUnder       Status = "under"
	StatusUnagreed    Status = "unagreed"
	StatusAmbiguous   Status = "ambiguous"
	StatusUnpriceable Status = "unpriceable"
)

// Reading is one way of pricing a line under the terms.
type Reading struct {
	Service       string   `json:"service"`
	ExpectedCents Cents    `json:"expectedCents"`
	Basis         []string `json:"basis"` // rule IDs
	Explanation   string   `json:"explanation"`
}

// LineResult is the engine's verdict on one line.
type LineResult struct {
	Line          Line      `json:"line"`
	Status        Status    `json:"status"`
	ExpectedCents Cents     `json:"expectedCents"`
	Readings      []Reading `json:"readings,omitempty"` // first = the reading used; others = alternatives
	Basis         []string  `json:"basis,omitempty"`
	Explanation   string    `json:"explanation"`
	Info          []string  `json:"info,omitempty"` // non-blocking notes
	TermsLabel    string    `json:"termsLabel,omitempty"`
}
