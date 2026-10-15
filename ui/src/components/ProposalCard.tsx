import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError } from "../api";
import type { BillProposal, Proposal, Source, TermsProposal } from "../types";
import { money, dateName, periodName } from "../money";
import { RulesTable } from "./Rules";
import { lineWhat } from "./BillCheckTable";
import { SourceViewer } from "./SourceViewer";
import { useHelpers, invalidateHelpers } from "./useHelpers";
import { MoneyInput, TextInput } from "./MoneyInput";

function isTerms(p: Proposal): p is Proposal & { payload: TermsProposal } { return p.kind === "terms"; }

function useSources(ids: string[]) {
  const [sources, setSources] = useState<Source[]>([]);
  useEffect(() => {
    let live = true;
    Promise.all(ids.map(id => api.source(id).then(r => r.source).catch(() => null))).then(ss => { if (live) setSources(ss.filter(Boolean) as Source[]); });
    return () => { live = false; };
  }, [ids.join(",")]);
  return sources;
}

export function ProposalCard({ proposal, onDone }: { proposal: Proposal; onDone: (result: { kind: string; id?: string }) => void }) {
  // An edited card points at its replacement: show that one in its place.
  const [replacement, setReplacement] = useState<Proposal | null>(null);
  useEffect(() => {
    if (proposal.status === "edited" && proposal.resultId) api.proposal(proposal.resultId).then(setReplacement).catch(() => {});
    else setReplacement(null);
  }, [proposal.status, proposal.resultId]);
  if (replacement) return <ProposalCard proposal={replacement} onDone={onDone} />;
  return <ProposalCardInner proposal={proposal} onDone={onDone} />;
}

function ProposalCardInner({ proposal, onDone }: { proposal: Proposal; onDone: (result: { kind: string; id?: string }) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<any>(() => structuredClone(proposal.payload));
  const [confirmedHelperId, setConfirmedHelperId] = useState("");
  const [showSources, setShowSources] = useState(false);
  const helpers = useHelpers();
  const closed = proposal.status !== "open";
  const sources = useSources(showSources ? proposal.payload.sourceIds ?? [] : []);

  const who = (p: { helperId?: string; newHelper?: { name?: string; role?: string; payMethod?: string } }) =>
    p.newHelper ? `${p.newHelper.name} (new helper, ${p.newHelper.role}${p.newHelper.payMethod ? `, paid by ${p.newHelper.payMethod}` : ""})` : helpers[p.helperId ?? ""]?.name ?? "…";

  // After a successful confirm or reject the buttons stay disabled until the
  // parent refetches the card and it renders as closed; only a failure re-enables them.
  async function confirm() {
    setBusy(true); setError("");
    let ok = false;
    try {
      const res = await api.confirm(proposal.id, proposal.contentHash, editing ? draft : undefined);
      ok = true;
      invalidateHelpers();
      setEditing(false);
      if (res.kind === "terms" && res.termsVersion?.helperId) setConfirmedHelperId(res.termsVersion.helperId);
      onDone({ kind: res.kind, id: res.kind === "bill" ? res.bill?.id : res.termsVersion?.helperId });
    } catch (e) {
      const ae = e as ApiError;
      setError(ae.code === "hash_mismatch" ? "This card changed since you opened it. Reload the page and try again." : ae.message);
    } finally { if (!ok) setBusy(false); }
  }
  async function reject() {
    setBusy(true); setError("");
    let ok = false;
    try { await api.reject(proposal.id); ok = true; onDone({ kind: "rejected" }); } catch (e) { setError((e as Error).message); } finally { if (!ok) setBusy(false); }
  }

  const header = (
    <div className="row">
      <span className={`badge ${proposal.kind === "terms" ? "terms" : "bill"}`}>{proposal.kind === "terms" ? "Terms proposal" : "Bill proposal"}</span>
      {proposal.proposedBy === "user" && <span className="badge info">edited by you</span>}
      {closed && <span className={`badge ${proposal.status === "confirmed" ? "good" : ""}`} title={proposal.status === "superseded" ? "A newer card replaced this one" : undefined}>{proposal.status === "superseded" ? "replaced by a newer card" : proposal.status === "edited" ? "edited" : proposal.status}</span>}
      <span className="small muted">{new Date(proposal.createdAt).toLocaleString()}</span>
      <span className="grow" />
      {proposal.payload.sourceIds?.length ? <button className="link small" onClick={() => setShowSources(s => !s)}>{showSources ? "Hide source" : `Show source (${proposal.payload.sourceIds.length})`}</button> : null}
    </div>
  );
  const sourceBlock = showSources && sources.length > 0 && <div className="stack" style={{ borderTop: "1px solid var(--line)", paddingTop: 10 }}>{sources.map(s => <SourceViewer key={s.id} source={s} />)}</div>;

  if (isTerms(proposal)) {
    const p = editing ? (draft as TermsProposal) : proposal.payload;
    return (
      <div className={`card proposal terms stack ${closed ? "closed" : ""}`}>
        {header}
        <div><strong>{who(p)}</strong> · terms from {p.terms.effectiveFrom ? dateName(p.terms.effectiveFrom) : <em>undated note</em>}</div>
        <p style={{ margin: 0 }}>{p.summary}</p>
        {editing ? <EditRules draft={draft} setDraft={setDraft} /> : <RulesTable terms={p.terms} />}
        {p.diff?.length ? <div><strong className="small">Changes vs current terms</strong><ul className="plain small diff">{p.diff.map((d, i) => <li key={i}>{d}</li>)}</ul></div> : null}
        {p.conflicts?.length ? <div><strong className="small">Sources disagree</strong><ul className="plain small">{p.conflicts.map((d, i) => <li key={i}>{d}</li>)}</ul></div> : null}
        {p.assumptions?.length ? <div><strong className="small">Assumptions</strong><ul className="plain small">{p.assumptions.map((d, i) => <li key={i}>{d}</li>)}</ul></div> : null}
        {sourceBlock}
        {error && <div className="error small">{error}</div>}
        {!closed && (
          <div className="row">
            <button className="primary" disabled={busy} onClick={confirm}>{editing ? "Confirm with my edits" : "Confirm these terms"}</button>
            <button disabled={busy} onClick={() => { setEditing(!editing); setDraft(structuredClone(proposal.payload)); }}>{editing ? "Cancel edits" : "Edit amounts & dates"}</button>
            <button className="danger" disabled={busy} onClick={reject}>Reject</button>
          </div>
        )}
        {closed && proposal.status === "confirmed" && (() => { const hid = p.helperId || confirmedHelperId || Object.values(helpers).find(h => h.name === p.newHelper?.name)?.id; return <Link className="small" to={hid ? `/helpers/${hid}` : "/helpers"}>{hid ? "View helper" : "See Helpers"}</Link>; })()}
      </div>
    );
  }

  const p = (editing ? draft : proposal.payload) as BillProposal;
  return (
    <div className={`card proposal bill stack ${closed ? "closed" : ""}`}>
      {header}
      <div><strong>{who(p)}</strong> · {periodName(p.bill.period)} · bill says {money(p.bill.claimedTotalCents)}</div>
      {p.summary && <p style={{ margin: 0 }}>{p.summary}</p>}
      <table>
        <thead><tr><th>Date</th><th>What</th><th className="num">Billed</th></tr></thead>
        <tbody>
          {p.bill.lines.map((l, i) => (
            <tr key={i}>
              <td>{editing ? <TextInput style={{ maxWidth: 130 }} value={l.date || ""} placeholder="YYYY-MM-DD" onChange={v => { const d = structuredClone(draft); d.bill.lines[i].date = v; setDraft(d); }} /> : dateName(l.date) || "—"}</td>
              <td>{lineWhat(l)}{l.note ? <div className="why">“{l.note}”</div> : null}</td>
              <td className="num">{editing ? <MoneyInput style={{ maxWidth: 110, textAlign: "right" }} cents={l.claimedCents} onChange={v => { const d = structuredClone(draft); d.bill.lines[i].claimedCents = v; setDraft(d); }} /> : money(l.claimedCents)}</td>
            </tr>
          ))}
          {editing && <tr><td></td><td>Bill total</td><td className="num"><MoneyInput style={{ maxWidth: 110, textAlign: "right" }} cents={p.bill.claimedTotalCents} onChange={v => { const d = structuredClone(draft); d.bill.claimedTotalCents = v; setDraft(d); }} /></td></tr>}
        </tbody>
      </table>
      {p.assumptions?.length ? <div><strong className="small">Assumptions</strong><ul className="plain small">{p.assumptions.map((d, i) => <li key={i}>{d}</li>)}</ul></div> : null}
      {sourceBlock}
      {error && <div className="error small">{error}</div>}
      {!closed && (
        <div className="row">
          <button className="primary" disabled={busy} onClick={confirm}>{editing ? "Confirm with my edits" : "Confirm this bill"}</button>
          <button disabled={busy} onClick={() => { setEditing(!editing); setDraft(structuredClone(proposal.payload)); }}>{editing ? "Cancel edits" : "Edit amounts & dates"}</button>
          <button className="danger" disabled={busy} onClick={reject}>Reject</button>
        </div>
      )}
      {closed && proposal.resultId && proposal.kind === "bill" && <Link className="small" to={`/bills/${proposal.resultId}`}>View bill check</Link>}
    </div>
  );
}

function EditRules({ draft, setDraft }: { draft: TermsProposal; setDraft: (d: any) => void }) {
  const update = (fn: (d: TermsProposal) => void) => { const d = structuredClone(draft); fn(d); setDraft(d); };
  return (
    <div className="stack">
      <div className="row"><label style={{ margin: 0 }}>Effective from</label><TextInput style={{ maxWidth: 160 }} value={draft.terms.effectiveFrom} placeholder="YYYY-MM-DD" onChange={v => update(d => { d.terms.effectiveFrom = v; })} /></div>
      <table>
        <thead><tr><th>What</th><th>Amount / rate ($)</th><th>Factor</th></tr></thead>
        <tbody>
          {draft.terms.rules.map((r, i) => (
            <tr key={r.id || i}>
              <td>{r.label || r.service || r.kind}</td>
              <td>
                {r.kind === "hourly" ? <MoneyInput style={{ maxWidth: 110 }} cents={r.rateCents ?? 0} onChange={v => update(d => { d.terms.rules[i].rateCents = v; })} /> :
                  r.kind === "adder" ? <MoneyInput style={{ maxWidth: 110 }} cents={r.perHourCents ?? 0} onChange={v => update(d => { d.terms.rules[i].perHourCents = v; })} /> :
                    r.kind === "multiplier" || r.kind === "rounding" || r.kind === "minimum" ? <span className="muted">—</span> :
                      <MoneyInput style={{ maxWidth: 110 }} cents={r.amountCents ?? 0} onChange={v => update(d => { d.terms.rules[i].amountCents = v; })} />}
              </td>
              <td>{r.kind === "multiplier" ? <TextInput style={{ maxWidth: 80 }} value={String(r.factor ?? 1)} onChange={v => update(d => { d.terms.rules[i].factor = parseFloat(v) || 1; })} /> : <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="small muted">Anything beyond amounts and dates: reject the card and tell the assistant what's different.</div>
    </div>
  );
}
