export type Cents = number;

export type Window = { days?: string[]; from?: string; to?: string; dates?: string; exactDates?: string[] };
export type Rule = {
  id: string; kind: string; service?: string; label?: string;
  rateCents?: Cents; amountCents?: Cents; factor?: number; perHourCents?: Cents; unit?: string;
  from?: string; until?: string; when?: Window; hours?: number; per?: string; percentOfBooking?: number;
  trigger?: { sameDay?: boolean; cancelledWithinHours?: number; holiday?: boolean };
  capCents?: Cents; receiptRequired?: boolean; period?: string; minutes?: number;
};
export type Terms = { helperId?: string; effectiveFrom: string; effectiveTo?: string; rules: Rule[]; defaultPerVisit?: string; label?: string };

export type Helper = {
  id: string; name: string; role: string; payMethod?: string; usualDays?: string[]; notes?: string; createdAt: string;
  currentTerms?: TermsVersion; termsCount?: number; openBills?: number; pendingProposals?: number;
};
export type Source = { id: string; kind: string; s3Key?: string; contentType?: string; text?: string; senderLabel?: string; messageDate?: string; uploadedAt: string };
export type TermsVersion = { id: string; helperId: string; terms: Terms; sourceIds: string[]; summary: string; assumptions?: string[]; proposalId: string; confirmedAt: string; confirmedBy: string; contentHash: string };

export type Line = { date?: string; service?: string; qty?: number; start?: string; end?: string; claimedCents: Cents; note?: string; flags?: Record<string, boolean>; noticeHours?: number };
export type Reading = { service: string; expectedCents: Cents; basis: string[]; explanation: string };
export type LineResult = { line: Line; status: string; expectedCents: Cents; readings?: Reading[]; basis?: string[]; explanation: string; info?: string[]; termsLabel?: string };
export type Decision = { lineIndex: number; status: string; readings?: Reading[]; claim: string; why: string };
export type Flag = { code: string; message: string; level: string };
export type CheckResult = {
  lines: LineResult[]; claimedTotalCents: Cents; lineSumCents: Cents; agreedTotalCents: Cents; arithmeticOk: boolean;
  pendingDecisions: Decision[]; flags: Flag[]; headline: string; problems: number; engineVersion: string; termsUsed: string[];
};
export type Approval = { amountCents: Cents; method: string; note?: string; includedPending: number[]; excludedPending: number[]; pendingAmounts?: Record<string, Cents>; at: string; by: string; checkHash: string };
export type Bill = {
  id: string; helperId: string; period: string; bill: { period: string; lines: Line[]; claimedTotalCents: Cents }; sourceIds: string[]; assumptions?: string[];
  status: "awaiting_terms" | "checked" | "approved" | "held"; check?: CheckResult; checkHash?: string; holdNote?: string; approval?: Approval; createdAt: string; updatedAt: string;
};
export type BillView = { bill: Bill; helper: Helper; sources: Source[]; hash: string; message: string };

export type TermsProposal = { helperId?: string; newHelper?: Partial<Helper>; terms: Terms; sourceIds: string[]; summary: string; conflicts?: string[]; assumptions?: string[]; diff?: string[] };
export type BillProposal = { helperId?: string; newHelper?: Partial<Helper>; bill: { period: string; lines: Line[]; claimedTotalCents: Cents }; sourceIds: string[]; summary: string; assumptions?: string[] };
export type Proposal = { id: string; kind: "terms" | "bill"; turnId?: string; payload: TermsProposal | BillProposal; contentHash: string; proposedBy: string; status: string; summary: string; createdAt: string; resultId?: string };

export type Turn = { id: string; sessionId: string; userText: string; sourceIds?: string[]; status: "queued" | "running" | "done" | "failed"; progress?: string; reply?: string; proposalIds?: string[]; error?: string; createdAt: string; updatedAt: string };
export type MemoryRecord = { id: string; text: string; namespace: string; createdAt?: string };
export type AuditEvent = { ts: string; actor: string; action: string; entity: string; entityId: string; hash?: string; detail?: string };

export type SessionSummary = { id: string; title: string; firstAt: string; lastAt: string; turns: number };
