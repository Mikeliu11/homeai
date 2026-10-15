import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api";
import type { AuditEvent, Bill, Helper, Proposal, Source, TermsVersion } from "../types";
import { RulesTable } from "../components/Rules";
import { SourceList } from "../components/SourceViewer";
import { ProposalCard } from "../components/ProposalCard";
import { dateName, money, periodName } from "../money";

export default function HelperDetail() {
  const { id } = useParams();
  const [data, setData] = useState<{ helper: Helper; versions: TermsVersion[]; bills: Bill[]; proposals: Proposal[]; sources: Source[]; audit: AuditEvent[] } | null>(null);
  const [err, setErr] = useState("");
  const load = useCallback(() => { if (id) api.helper(id).then(setData).catch(e => setErr(e.message)); }, [id]);
  useEffect(load, [load]);
  if (err) return <div className="error">{err}</div>;
  if (!data) return <p className="muted">Loading…</p>;
  const { helper, versions, bills, proposals, sources, audit } = data;
  const today = new Date().toISOString().slice(0, 10);
  const current = [...versions].filter(v => v.terms.effectiveFrom <= today).sort((a, b) => (a.terms.effectiveFrom < b.terms.effectiveFrom ? 1 : -1))[0] ?? versions[versions.length - 1];
  const usedSources = new Map(sources.map(s => [s.id, s]));
  return (
    <div className="stack">
      <div className="row">
        <h1 style={{ margin: 0 }}>{helper.name}</h1>
        <span className="badge">{helper.role}</span>
        {helper.payMethod && <span className="badge">paid by {helper.payMethod}</span>}
        {helper.usualDays?.length ? <span className="badge">usually {helper.usualDays.map(d => d[0].toUpperCase() + d.slice(1)).join(", ")}</span> : null}
      </div>

      {proposals.length > 0 && (
        <div className="stack">
          <h2 style={{ margin: 0 }}>Waiting for your confirmation</h2>
          {proposals.map(p => <ProposalCard key={p.id} proposal={p} onDone={load} />)}
        </div>
      )}

      <div className="split">
        <div className="stack">
          <h2 style={{ margin: 0 }}>Agreed terms</h2>
          {!current ? <div className="card muted">No confirmed terms yet. Paste the thread where you agreed rates on <Link to="/home">Home</Link>.</div> : (
            <div className="card stack">
              <div className="row"><strong>{(current.terms.label || "Current terms").replace(/\d{4}-\d{2}-\d{2}/g, d => dateName(d))}</strong><span className="badge good">in force</span><span className="small muted">confirmed {new Date(current.confirmedAt).toLocaleDateString()} by {current.confirmedBy}</span></div>
              <p style={{ margin: 0 }}>{current.summary}</p>
              <RulesTable terms={current.terms} />
              {current.assumptions?.length ? <div className="small"><strong>Assumptions:</strong> {current.assumptions.join(" ")}</div> : null}
            </div>
          )}
          {versions.length > 1 && (
            <div className="card stack">
              <strong>All versions</strong>
              {[...versions].sort((a, b) => (a.terms.effectiveFrom < b.terms.effectiveFrom ? 1 : -1)).map(v => (
                <details key={v.id}>
                  <summary className="small">{v.terms.effectiveFrom ? `From ${dateName(v.terms.effectiveFrom)}` : "Undated note"} — {v.summary} <span className="muted">(confirmed {new Date(v.confirmedAt).toLocaleDateString()})</span></summary>
                  <div style={{ marginTop: 8 }}><RulesTable terms={v.terms} /><div style={{ marginTop: 8 }}><SourceList sources={(v.sourceIds ?? []).map(sid => usedSources.get(sid)).filter(Boolean) as Source[]} /></div></div>
                </details>
              ))}
            </div>
          )}
          <h2 style={{ margin: 0 }}>Bills</h2>
          {bills.length === 0 ? <div className="card muted">No bills yet.</div> : bills.map(b => (
            <Link key={b.id} to={`/bills/${b.id}`} className="card row" style={{ color: "inherit" }}>
              <strong>{periodName(b.period)}</strong>
              <span className="grow small">{b.check?.headline ?? (b.status === "awaiting_terms" ? "waiting for terms" : "")}</span>
              <span className={`badge ${b.status === "approved" ? "good" : b.status === "held" ? "warn" : "info"}`}>{b.status === "approved" ? `approved ${money(b.approval?.amountCents)}` : b.status.replace("_", " ")}</span>
            </Link>
          ))}
        </div>
        <div className="stack">
          <h2 style={{ margin: 0 }}>Where the terms come from</h2>
          <div className="card"><SourceList sources={current ? (current.sourceIds ?? []).map(sid => usedSources.get(sid)).filter(Boolean) as Source[] : []} /></div>
          <h2 style={{ margin: 0 }}>Activity</h2>
          <div className="card">
            {audit.length === 0 ? <span className="muted small">Nothing yet.</span> : <ul className="plain small">{audit.map((a, i) => <li key={i}><span className="muted">{new Date(a.ts).toLocaleString()}</span> · {a.action.replace(".", " ")} {a.detail ? `· ${a.detail.replace(/\d{4}-\d{2}-\d{2}/g, d => dateName(d))}` : ""}</li>)}</ul>}
          </div>
        </div>
      </div>
    </div>
  );
}
