import type { CheckResult, Line } from "../types";
import { money, dateName, clock } from "../money";

export function lineWhat(l: Line): string {
  const span = l.start || l.end ? `${clock(l.start)}–${clock(l.end)}` : "";
  const qty = l.qty ? `${l.qty} × ` : "";
  const svc = (l.service || "").replace(/_/g, " ");
  return [qty + svc, span].filter(Boolean).join(" ");
}

const badge: Record<string, { cls: string; text: string }> = {
  match: { cls: "good", text: "as agreed" },
  over: { cls: "bad", text: "over" },
  under: { cls: "info", text: "under" },
  unagreed: { cls: "warn", text: "not agreed" },
  ambiguous: { cls: "warn", text: "two readings" },
  unpriceable: { cls: "warn", text: "can't price" },
};

export function BillCheckTable({ check, claimedTotal }: { check: CheckResult; claimedTotal: number }) {
  return (
    <div className="table-wrap">
    <table>
      <thead>
        <tr><th>Date</th><th>What</th><th className="num">Billed</th><th className="num">Agreed</th></tr>
      </thead>
      <tbody>
        {check.lines.map((lr, i) => ({ lr, i })).sort((a, b) => (a.lr.line.date || "9999") < (b.lr.line.date || "9999") ? -1 : (a.lr.line.date || "9999") > (b.lr.line.date || "9999") ? 1 : a.i - b.i).map(({ lr, i }) => {
          const b = badge[lr.status] ?? { cls: "", text: lr.status };
          const showExpected = ["match", "over", "under", "ambiguous"].includes(lr.status);
          return (
            <tr key={i}>
              <td>{dateName(lr.line.date) || <span className="muted">{check.lines.length > 1 ? "—" : ""}</span>}</td>
              <td>
                <div className="row" style={{ gap: 8 }}><span>{lineWhat(lr.line) || lr.line.note}</span><span className={`badge ${b.cls}`}>{b.text}</span></div>
                {lr.line.note && lineWhat(lr.line) ? <div className="why">“{lr.line.note}”</div> : null}
                <div className="why">{lr.explanation}</div>
                {(lr.info ?? []).map((t, k) => <div className="why" key={k}>ℹ︎ {t}</div>)}
              </td>
              <td className="num">{money(lr.line.claimedCents)}</td>
              <td className="num">{showExpected ? money(lr.expectedCents) : <span className="muted">—</span>}</td>
            </tr>
          );
        })}
        <tr className="total">
          <td></td>
          <td>Lines add to {money(check.lineSumCents)}{check.arithmeticOk ? "" : <> <span className="badge bad" style={{ marginLeft: 8 }}>bill says {money(claimedTotal)}</span></>}<div className="why">Lines billed below the agreed rate count at what was billed.</div></td>
          <td className="num">{money(claimedTotal)}</td>
          <td className="num">{money(check.agreedTotalCents)}<div className="why">to pay</div></td>
        </tr>
      </tbody>
    </table>
    </div>
  );
}
