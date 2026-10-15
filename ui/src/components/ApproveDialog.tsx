import { useMemo, useState } from "react";
import type { Bill, Helper } from "../types";
import { money, parseMoney } from "../money";
import { lineWhat } from "./BillCheckTable";

export type ApproveChoice = { amountCents?: number; method: string; note: string; includedPending: number[]; excludedPending: number[]; pendingAmounts: Record<number, number> };
type Decision = { kind: "include" } | { kind: "exclude" } | { kind: "reading"; cents: number };

export function ApproveDialog({ bill, helper, onClose, onApprove, busy, error }: {
  bill: Bill; helper: Helper; onClose: () => void; onApprove: (c: ApproveChoice) => void; busy: boolean; error?: string;
}) {
  const raw = bill.check!;
  const check = { ...raw, pendingDecisions: raw.pendingDecisions ?? [], flags: raw.flags ?? [], lines: raw.lines ?? [] };
  const [decisions, setDecisions] = useState<Record<number, Decision | undefined>>({});
  const [method, setMethod] = useState(helper.payMethod || "");
  const [note, setNote] = useState("");
  const [typed, setTyped] = useState("");
  const needsTyped = check.pendingDecisions.some(d => d.status === "unpriceable" && decisions[d.lineIndex]?.kind === "include");
  const allDecided = check.pendingDecisions.every(d => decisions[d.lineIndex]);
  const computed = useMemo(() => {
    let amt = check.agreedTotalCents;
    for (const d of check.pendingDecisions) {
      const dec = decisions[d.lineIndex];
      if (dec?.kind === "include") amt += bill.bill.lines[d.lineIndex].claimedCents;
      if (dec?.kind === "reading") amt += dec.cents;
    }
    return amt;
  }, [check, decisions, bill]);
  // a typed amount must be a positive number: the server reads 0 as "not given"
  const parsed = typed ? parseMoney(typed) : null;
  const typedCents = parsed !== null && parsed > 0 ? parsed : null;
  const differs = typedCents !== null && typedCents !== computed;
  const amount = typed ? typedCents : computed;
  const canApprove = allDecided && method.trim() !== "" && (typed === "" || typedCents !== null)
    && (!needsTyped || (typedCents !== null && typedCents > 0 && note.trim() !== ""))
    && (!differs || note.trim() !== "");
  const set = (i: number, d: Decision) => setDecisions({ ...decisions, [i]: d });
  const is = (i: number, k: Decision["kind"], cents?: number) => { const d = decisions[i]; return d?.kind === k && (k !== "reading" || (d as any).cents === cents); };

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal stack" onClick={e => e.stopPropagation()}>
        <h2 style={{ margin: 0 }}>Approve {helper.name}'s bill</h2>
        <p className="muted small" style={{ margin: 0 }}>Nothing is sent anywhere. This records what you decided to pay and locks the bill.</p>

        {check.pendingDecisions.length > 0 && (
          <div className="stack">
            <strong>Decide on each flagged line</strong>
            {check.pendingDecisions.map(d => {
              const line = bill.bill.lines[d.lineIndex];
              const i = d.lineIndex;
              return (
                <div className="pending stack" key={i}>
                  <div><strong>{lineWhat(line) || line.note}</strong> · billed {money(line.claimedCents)}</div>
                  <div className="small">{d.why}</div>
                  <div className="row">
                    {d.status === "ambiguous" && (d.readings ?? []).map((r, k) => (
                      <button key={k} className={is(i, "reading", r.expectedCents) ? "primary" : ""} onClick={() => set(i, { kind: "reading", cents: r.expectedCents })} title={r.explanation}>
                        Pay {money(r.expectedCents)} ({r.service.replace(/_/g, " ")}{k === 0 ? "" : ", alt"})
                      </button>
                    ))}
                    <button className={is(i, "include") ? "primary" : ""} onClick={() => set(i, { kind: "include" })}>Pay as billed ({money(line.claimedCents)})</button>
                    <button className={is(i, "exclude") ? "primary" : ""} onClick={() => set(i, { kind: "exclude" })}>Leave it out</button>
                  </div>
                  {d.status === "unagreed" && <span className="small muted">Paying it does not change the agreement. Ask the assistant to add it to the terms if it should be standard.</span>}
                  {d.status === "unpriceable" && <span className="small muted">The engine could not price this line. If you pay it, type the total and say how you got there.</span>}
                </div>
              );
            })}
          </div>
        )}

        <div className="row" style={{ alignItems: "flex-start" }}>
          <div className="grow"><label>Amount <span className="muted small">(blank = {money(computed)})</span></label>
            <input value={typed} placeholder={money(computed)} onChange={e => setTyped(e.target.value)} />
            <div className="small muted">{typed ? (typedCents === null ? "Enter a dollar amount above $0" : differs ? `Differs from the computed ${money(computed)}; a note is required` : "Same as computed") : `Computed from the agreement${check.pendingDecisions.length ? " and your decisions above" : ""}`}</div>
          </div>
          <div className="grow"><label>Pay by</label><input value={method} onChange={e => setMethod(e.target.value)} placeholder="Venmo, Zelle, cash…" /></div>
        </div>
        <div><label>Note {needsTyped || differs ? "(required)" : "(optional)"} <span className="muted small">— goes into the message to the helper</span></label><input value={note} onChange={e => setNote(e.target.value)} /></div>
        {error && <div className="error">{error}</div>}
        <div className="row" style={{ justifyContent: "flex-end" }}>
          <button onClick={onClose} disabled={busy}>Cancel</button>
          <button className="primary" disabled={!canApprove || busy} onClick={() => onApprove({
            amountCents: typed ? typedCents ?? undefined : undefined, method, note,
            includedPending: Object.entries(decisions).filter(([, v]) => v?.kind === "include").map(([k]) => Number(k)),
            excludedPending: Object.entries(decisions).filter(([, v]) => v?.kind === "exclude").map(([k]) => Number(k)),
            pendingAmounts: Object.fromEntries(Object.entries(decisions).filter(([, v]) => v?.kind === "reading").map(([k, v]) => [Number(k), (v as any).cents])),
          })}>
            {busy ? "Approving…" : `Approve ${amount !== null && amount !== undefined ? money(amount) : ""}`}
          </button>
        </div>
      </div>
    </div>
  );
}
