package records

import "context"

// Store is the persistence boundary. The in-memory implementation reproduces
// the conditional rules the DynamoDB one enforces with condition expressions:
// an approved bill cannot be written, a proposal id is idempotent, a turn can
// be claimed once.
type Store interface {
	PutHelper(ctx context.Context, h Helper) error
	GetHelper(ctx context.Context, id string) (Helper, error)
	ListHelpers(ctx context.Context) ([]Helper, error)

	PutSource(ctx context.Context, s Source) error
	GetSource(ctx context.Context, id string) (Source, error)

	PutTermsVersion(ctx context.Context, v TermsVersion) error
	ListTermsVersions(ctx context.Context, helperID string) ([]TermsVersion, error)

	// PutBill writes a bill unless the stored one is approved (ErrAlreadyApproved).
	PutBill(ctx context.Context, b BillRecord) error
	GetBill(ctx context.Context, id string) (BillRecord, error)
	ListBills(ctx context.Context, helperID string) ([]BillRecord, error) // "" = all
	// ApproveBill sets the approval only if the bill is not approved and its CheckHash still equals expectedCheckHash.
	ApproveBill(ctx context.Context, id string, expectedCheckHash string, a Approval) (BillRecord, error)

	// PutProposal is idempotent on ID: an existing proposal is left untouched and returned as-is.
	PutProposal(ctx context.Context, p Proposal) (Proposal, error)
	GetProposal(ctx context.Context, id string) (Proposal, error)
	ListProposals(ctx context.Context, status string) ([]Proposal, error) // "" = all
	// CloseProposal moves open → status; ErrProposalClosed if it is not open.
	CloseProposal(ctx context.Context, id, status, resultID, at string) (Proposal, error)

	PutTurn(ctx context.Context, t Turn) error
	GetTurn(ctx context.Context, id string) (Turn, error)
	ListTurns(ctx context.Context, sessionID string) ([]Turn, error)
	// ClaimTurn moves queued → running; false if it was not queued.
	ClaimTurn(ctx context.Context, id, at string) (bool, error)
	UpdateTurn(ctx context.Context, t Turn) error

	AppendAudit(ctx context.Context, e AuditEvent) error
	ListAudit(ctx context.Context) ([]AuditEvent, error)

	// OpenTurns lists turns that are queued or running, across sessions.
	OpenTurns(ctx context.Context) ([]Turn, error)
	// ListSessions summarises every conversation, newest first.
	ListSessions(ctx context.Context) ([]SessionSummary, error)
	// DeleteSession removes a conversation's turns (proposals and records are untouched).
	DeleteSession(ctx context.Context, sessionID string) error
	// Reset deletes every record: helpers, terms, bills, approvals, proposals,
	// turns, sources and audit. Only the household reset endpoint calls it.
	Reset(ctx context.Context) error
}
