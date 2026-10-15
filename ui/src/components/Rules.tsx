import type { Rule, Terms } from "../types";
import { money, clock } from "../money";

export function ruleSentence(r: Rule): string {
  const w = r.when;
  const when = w
    ? [w.days?.length ? w.days.map(d => (d[0] ?? "").toUpperCase() + d.slice(1)).join("/") : "", w.from ? `from ${clock(w.from)}` : "", w.to ? `to ${clock(w.to)}` : "", w.dates === "us_federal_holidays" ? "on federal holidays" : "", w.exactDates?.length ? `on ${w.exactDates.join(", ")}` : ""].filter(Boolean).join(" ")
    : "";
  switch (r.kind) {
    case "hourly": return `${money(r.rateCents)}/hr${when ? " " + when : ""}`;
    case "multiplier": return `×${r.factor} ${when}`;
    case "adder": return `+${money(r.perHourCents)}/hr per ${r.per?.replace("_", " ") ?? "unit"}${when ? " " + when : ""}`;
    case "flat": return `${money(r.amountCents)} flat${r.from ? ` from ${clock(r.from)}` : ""}${r.until ? ` until ${clock(r.until)}` : ""}, hourly after`;
    case "per_unit": return `${money(r.amountCents)} per ${r.unit ?? "each"}`;
    case "minimum": return r.hours ? `${r.hours}-hour minimum per ${r.per ?? "booking"}` : `minimum ${money(r.amountCents)}`;
    case "fee": {
      const t = r.trigger ?? {};
      const amt = r.percentOfBooking ? `${r.percentOfBooking}% of the booking` : money(r.amountCents);
      if (t.sameDay) return `${amt} on same-day bookings`;
      if (t.cancelledWithinHours) return `${amt} for cancellations within ${t.cancelledWithinHours}h`;
      if (t.holiday) return `${amt} on holidays`;
      return `${amt} each time it applies`;
    }
    case "reimbursement": return `reimbursed at cost${r.capCents ? ` up to ${money(r.capCents)}` : ""}${r.receiptRequired ? ", receipt required" : ""}`;
    case "per_period": return `${money(r.amountCents)} per ${r.period ?? "period"}`;
    case "rounding": return `time rounds up to ${r.minutes} minutes`;
  }
  return r.kind;
}

export function RulesTable({ terms }: { terms: Terms }) {
  return (
    <table>
      <thead><tr><th>What</th><th>Rate</th><th title="The short name bill lines are matched on">Bill key</th></tr></thead>
      <tbody>
        {terms.rules.map(r => (
          <tr key={r.id}>
            <td>{r.label || r.service || r.kind}</td>
            <td>{ruleSentence(r)}</td>
            <td className="small muted">{r.service || <span>—</span>}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function termsOneLiner(terms: Terms): string {
  return terms.rules.map(r => `${r.label || r.service || r.kind}: ${ruleSentence(r)}`).join("; ");
}
