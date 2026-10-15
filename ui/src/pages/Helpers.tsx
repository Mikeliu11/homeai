import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { Helper, Proposal } from "../types";
import { ProposalCard } from "../components/ProposalCard";
import { termsOneLiner } from "../components/Rules";
import { dateName } from "../money";

export default function Helpers() {
  const [helpers, setHelpers] = useState<Helper[] | null>(null);
  const [pending, setPending] = useState<Proposal[]>([]);
  const [err, setErr] = useState("");
  const load = () => {
    setErr("");
    api.helpers().then(setHelpers).catch(e => setErr(e.message));
    api.proposals("open").then(p => setPending(p.filter(x => x.kind === "terms"))).catch(() => {});
  };
  useEffect(load, []);
  return (
    <div className="stack">
      <h1 style={{ margin: 0 }}>Helpers</h1>
      {err && <div className="error">{err}</div>}
      {pending.length > 0 && (
        <div className="stack">
          <h2 style={{ margin: 0 }}>Waiting for your confirmation</h2>
          <p className="small muted" style={{ margin: 0 }}>Terms the assistant drafted. Nothing is agreed until you confirm.</p>
          {pending.map(p => <ProposalCard key={p.id} proposal={p} onDone={load} />)}
          {helpers && helpers.length > 0 && <h2 style={{ margin: "12px 0 0" }}>Helpers on record</h2>}
        </div>
      )}
      {helpers && helpers.length === 0 && pending.length === 0 && (
        <div className="card empty">
          <p><strong>No helpers yet.</strong></p>
          <p>Go to <Link to="/home">Home</Link> and paste what you have about one helper: a text thread, a screenshot, or a note from memory. The assistant drafts the terms; you confirm them.</p>
        </div>
      )}
      <div className="cards">
        {helpers?.map(h => (
          <Link key={h.id} to={`/helpers/${h.id}`} className="card stack" style={{ color: "inherit" }}>
            <div className="row">
              <strong style={{ fontSize: 17 }}>{h.name}</strong>
              <span className="badge">{h.role}</span>
              {h.payMethod && <span className="badge">{h.payMethod}</span>}
              <span className="grow" />
              {h.pendingProposals ? <span className="badge warn">{h.pendingProposals} to confirm</span> : null}
              {h.openBills ? <span className="badge info">{h.openBills} bill{h.openBills > 1 ? "s" : ""} to approve</span> : null}
            </div>
            {h.currentTerms ? (
              <>
                <div className="small">{h.currentTerms.summary || termsOneLiner(h.currentTerms.terms)}</div>
                <div className="small muted">Terms since {h.currentTerms.terms.effectiveFrom ? dateName(h.currentTerms.terms.effectiveFrom) : "(undated note)"} · {h.termsCount} version{h.termsCount === 1 ? "" : "s"}</div>
              </>
            ) : <div className="small muted">No confirmed terms yet.</div>}
          </Link>
        ))}
      </div>
    </div>
  );
}
