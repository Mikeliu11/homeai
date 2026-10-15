import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { Bill, Helper, Proposal } from "../types";
import { ProposalCard } from "../components/ProposalCard";
import { money, periodName } from "../money";

export default function Bills() {
  const [bills, setBills] = useState<Bill[] | null>(null);
  const [helpers, setHelpers] = useState<Record<string, Helper>>({});
  const [pending, setPending] = useState<Proposal[]>([]);
  const [err, setErr] = useState("");
  const load = () => {
    setErr("");
    Promise.all([api.bills(), api.helpers()])
      .then(([b, h]) => { setBills(b); setHelpers(Object.fromEntries(h.map(x => [x.id, x]))); })
      .catch(e => setErr(e.message));
    api.proposals("open").then(p => setPending(p.filter(x => x.kind === "bill"))).catch(() => {});
  };
  useEffect(load, []);
  return (
    <div className="stack">
      <h1 style={{ margin: 0 }}>Bills</h1>
      {err && <div className="error">{err}</div>}
      {pending.length > 0 && (
        <div className="stack">
          <h2 style={{ margin: 0 }}>Waiting for your confirmation</h2>
          <p className="small muted" style={{ margin: 0 }}>Drafted by the assistant, not yet a bill on record. Confirm to have the engine check it, or reject it.</p>
          {pending.map(p => <ProposalCard key={p.id} proposal={p} onDone={load} />)}
          {bills && bills.length > 0 && <h2 style={{ margin: "12px 0 0" }}>Bills on record</h2>}
        </div>
      )}
      {bills && bills.length === 0 && pending.length === 0 && <div className="card empty"><p><strong>No bills yet.</strong></p><p>Paste a helper's bill on <Link to="/home">Home</Link>. The assistant lays it out as lines, the engine checks every line against what you agreed.</p></div>}
      {bills?.map(b => (
        <Link key={b.id} to={`/bills/${b.id}`} className="card row" style={{ color: "inherit" }}>
          <strong>{helpers[b.helperId]?.name ?? "…"}</strong>
          <span>{periodName(b.period)}</span>
          <span className="grow small">{b.check?.headline ?? (b.status === "awaiting_terms" ? "waiting for confirmed terms" : "")}</span>
          <span className={`badge ${b.status === "approved" ? "good" : b.status === "held" ? "warn" : b.check && b.check.problems > 0 ? "warn" : "info"}`}>
            {b.status === "approved" ? `approved ${money(b.approval?.amountCents)}` : b.status.replace("_", " ")}
          </span>
        </Link>
      ))}
    </div>
  );
}
