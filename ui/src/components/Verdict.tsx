import type { Bill } from "../types";
import { money } from "../money";

export function Verdict({ bill }: { bill: Bill }) {
  if (bill.status === "approved" && bill.approval) {
    return (
      <div className="verdict approved">
        <div>Approved {money(bill.approval.amountCents)} via {bill.approval.method}</div>
        {bill.check && (
          <div className="figures">
            <div><span className="label">Bill said</span><span className="value">{money(bill.check.claimedTotalCents)}</span></div>
            <div><span className="label">Agreement supported</span><span className="value">{money(bill.check.agreedTotalCents)}</span></div>
            <div><span className="label">Approved</span><span className="value">{money(bill.approval.amountCents)}</span></div>
          </div>
        )}
        <span className="sub">On {new Date(bill.approval.at).toLocaleString()} by {bill.approval.by}. This bill is locked and cannot be edited.</span>
      </div>
    );
  }
  if (bill.status === "awaiting_terms") {
    return <div className="verdict bad">Waiting for confirmed terms<span className="sub">Confirm this helper's agreement and the check runs by itself.</span></div>;
  }
  const c = bill.check;
  if (!c) return null;
  const good = (c.problems ?? 0) === 0;
  return (
    <div className={`verdict ${good ? "good" : "bad"}`}>
      <div>{c.headline}</div>
      <div className="figures">
        <div><span className="label">Bill says</span><span className="value">{money(c.claimedTotalCents)}</span></div>
        <div><span className="label">Lines add to</span><span className="value">{money(c.lineSumCents)}</span></div>
        <div><span className="label">Agreement supports</span><span className="value">{money(c.agreedTotalCents)}</span></div>
      </div>
      {bill.status === "held" && <span className="sub">On hold{bill.holdNote ? `: ${bill.holdNote}` : ""}. Approve when you are ready; holding changes nothing else.</span>}
    </div>
  );
}
