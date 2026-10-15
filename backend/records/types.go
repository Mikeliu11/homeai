// Package records is what the system records: helpers, confirmed terms,
// bills, approvals and the audit trail. Nothing here is written by the
// model; the agent can only create proposals and turns.
package records

import (
	"encoding/json"
	"errors"
	"fmt"

	"helperpay/pricing"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrHashMismatch      = errors.New("what you confirmed is not what is stored; reload and try again")
	ErrAlreadyApproved   = errors.New("this bill is approved and can no longer change")
	ErrTermsChanged      = errors.New("the agreement changed since this bill was checked; review the new result")
	ErrPendingUnresolved = errors.New("every flagged line needs a decision before approval")
	ErrProposalClosed    = errors.New("this proposal was already confirmed or rejected")
	ErrAmountRequired    = errors.New("a typed amount and a note are required when a line could not be priced")
	ErrNoteRequired      = errors.New("say why the amount differs from what the agreement supports")
	ErrInvalid           = errors.New("invalid proposal")
	ErrRefused           = errors.New("refused") // a user-fixable refusal; the message says why
)

// refusal is an error the user can act on. Its text is shown as-is and it
// matches ErrRefused for the HTTP status.
type refusal struct{ msg string }

func (r refusal) Error() string        { return r.msg }
func (r refusal) Is(target error) bool { return target == ErrRefused }

func refuse(format string, a ...any) error { return refusal{fmt.Sprintf(format, a...)} }

type Helper struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Role      string        `json:"role"`
	PayMethod string        `json:"payMethod,omitempty"`
	UsualDays []pricing.Day `json:"usualDays,omitempty"`
	Notes     string        `json:"notes,omitempty"`
	CreatedAt string        `json:"createdAt"`
}

type Source struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // text | image | note
	S3Key       string `json:"s3Key,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Text        string `json:"text,omitempty"`
	SenderLabel string `json:"senderLabel,omitempty"`
	MessageDate string `json:"messageDate,omitempty"` // YYYY-MM-DD if known
	UploadedAt  string `json:"uploadedAt"`
}

// TermsVersion is a confirmed, immutable snapshot of a helper's agreement.
type TermsVersion struct {
	ID          string        `json:"id"`
	HelperID    string        `json:"helperId"`
	Terms       pricing.Terms `json:"terms"`
	SourceIDs   []string      `json:"sourceIds"`
	Summary     string        `json:"summary"`
	Assumptions []string      `json:"assumptions,omitempty"`
	ProposalID  string        `json:"proposalId"`
	ConfirmedAt string        `json:"confirmedAt"`
	ConfirmedBy string        `json:"confirmedBy"`
	ContentHash string        `json:"contentHash"`
}

type BillStatus string

const (
	BillAwaitingTerms BillStatus = "awaiting_terms"
	BillChecked       BillStatus = "checked"
	BillApproved      BillStatus = "approved"
	BillHeld          BillStatus = "held"
)

// Approval is Jordan's decision. Immutable once written.
type Approval struct {
	AmountCents     pricing.Cents         `json:"amountCents"`
	Method          string                `json:"method"`
	Note            string                `json:"note,omitempty"`
	IncludedPending []int                 `json:"includedPending"`
	ExcludedPending []int                 `json:"excludedPending"`
	PendingAmounts  map[int]pricing.Cents `json:"pendingAmounts,omitempty"` // a chosen reading for an ambiguous line
	TermsVersionIDs []string              `json:"termsVersionIds"`
	EngineVersion   string                `json:"engineVersion"`
	CheckSnapshot   pricing.CheckResult   `json:"checkSnapshot"`
	CheckHash       string                `json:"checkHash"`
	At              string                `json:"at"`
	By              string                `json:"by"`
}

type BillRecord struct {
	ID          string               `json:"id"`
	HelperID    string               `json:"helperId"`
	Period      string               `json:"period"`
	Bill        pricing.Bill         `json:"bill"`
	SourceIDs   []string             `json:"sourceIds"`
	Assumptions []string             `json:"assumptions,omitempty"`
	Status      BillStatus           `json:"status"`
	Check       *pricing.CheckResult `json:"check,omitempty"`
	CheckHash   string               `json:"checkHash,omitempty"`
	HoldNote    string               `json:"holdNote,omitempty"`
	Approval    *Approval            `json:"approval,omitempty"`
	ProposalID  string               `json:"proposalId"`
	CreatedAt   string               `json:"createdAt"`
	UpdatedAt   string               `json:"updatedAt"`
}

// ContentHashForConfirm is what the UI must echo back on approve: the bill as rendered.
func (b BillRecord) ContentHashForConfirm() string {
	return ContentHash(struct {
		ID    string
		Lines []pricing.Line
		Total pricing.Cents
		Check string
	}{b.ID, b.Bill.Lines, b.Bill.ClaimedTotalCents, b.CheckHash})
}

// Proposal is the only thing the agent writes. Immutable; confirm/reject change Status only.
type Proposal struct {
	ID          string          `json:"id"`   // idempotency key: turnID:toolIndex, or user:<rand>
	Kind        string          `json:"kind"` // terms | bill
	TurnID      string          `json:"turnId,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	ContentHash string          `json:"contentHash"`
	ProposedBy  string          `json:"proposedBy"` // agent | user
	Status      string          `json:"status"`     // open | confirmed | rejected
	Summary     string          `json:"summary"`
	CreatedAt   string          `json:"createdAt"`
	ClosedAt    string          `json:"closedAt,omitempty"`
	ResultID    string          `json:"resultId,omitempty"` // TermsVersion or Bill ID once confirmed
}

type TermsProposal struct {
	HelperID    string        `json:"helperId,omitempty"`
	NewHelper   *Helper       `json:"newHelper,omitempty"`
	Terms       pricing.Terms `json:"terms"`
	SourceIDs   []string      `json:"sourceIds"`
	Summary     string        `json:"summary"`
	Conflicts   []string      `json:"conflicts,omitempty"`
	Assumptions []string      `json:"assumptions,omitempty"`
	Diff        []string      `json:"diff,omitempty"` // human lines: "regular visit: $140 → $150"
}

type BillProposal struct {
	HelperID    string       `json:"helperId,omitempty"`
	NewHelper   *Helper      `json:"newHelper,omitempty"`
	Bill        pricing.Bill `json:"bill"`
	SourceIDs   []string     `json:"sourceIds"`
	Summary     string       `json:"summary"`
	Assumptions []string     `json:"assumptions,omitempty"`
}

type TurnStatus string

const (
	TurnQueued  TurnStatus = "queued"
	TurnRunning TurnStatus = "running"
	TurnDone    TurnStatus = "done"
	TurnFailed  TurnStatus = "failed"
)

// SessionSummary is one past conversation for the Home list.
type SessionSummary struct {
	ID      string `json:"id"`
	Title   string `json:"title"` // first user message, shortened
	FirstAt string `json:"firstAt"`
	LastAt  string `json:"lastAt"`
	Turns   int    `json:"turns"`
}

type Turn struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"sessionId"`
	UserText    string     `json:"userText"`
	SourceIDs   []string   `json:"sourceIds,omitempty"`
	Status      TurnStatus `json:"status"`
	Progress    string     `json:"progress,omitempty"`
	Reply       string     `json:"reply,omitempty"`
	ProposalIDs []string   `json:"proposalIds,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   string     `json:"createdAt"`
	UpdatedAt   string     `json:"updatedAt"`
}

type AuditEvent struct {
	TS       string `json:"ts"`
	Actor    string `json:"actor"`
	Action   string `json:"action"` // terms.confirmed bill.confirmed bill.approved bill.held proposal.rejected helper.created
	Entity   string `json:"entity"`
	EntityID string `json:"entityId"`
	Hash     string `json:"hash,omitempty"`
	Detail   string `json:"detail,omitempty"`
}
