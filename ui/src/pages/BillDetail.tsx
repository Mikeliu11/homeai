import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ApiError } from "../api";
import type { BillView } from "../types";
import { Verdict } from "../components/Verdict";
import { BillCheckTable, lineWhat } from "../components/BillCheckTable";
import { ApproveDialog, type ApproveChoice } from "../components/ApproveDialog";
import { SourceList } from "../components/SourceViewer";
import { money, periodName , dateName } from "../money";

export default function BillDetail() {
  const { id } = useParams();
  const [view, setView] = useState<BillView | null>(null);
  const [err, setErr] = useState(""); // the page could not load
  const [actionErr, setActionErr] = useState(""); // an action failed; the page stays
  const [approving, setApproving] = useState(false);
  const [busy, setBusy] = useState(false);
  const [dialogErr, setDialogErr] = useState("");
  const [holdNote, setHoldNote] = useState("");
  const [flash, setFlash] = useState("");
  const [copied, setCopied] = useState(false);
  const load = useCallback(() => { if (id) api.bill(id).then(setView).catch(e => setErr(e.message)); }, [id]);
  useEffect(load, [load]);
  if (err) return <div className="error">{err}</div>;
  if (!view) return <p className="muted">Loading…</p>;
  const { bill, helper, sources, hash, message } = view;

  async function approve(c: ApproveChoice) {
    if (!id) return;
    setBusy(true); setDialogErr("");
    try {
      const v = await api.approve(id, { hash, ...c });
      setView(v); setApproving(false);
    } catch (e) {
      const ae = e as ApiError;
      if (ae.code === "terms_changed" && ae.body?.bill) { setView(ae.body.bill); setApproving(false); setActionErr("The agreement changed since this bill was checked. Here is the new result; review it before approving."); }
      else if (ae.code === "hash_mismatch") { setDialogErr("This bill changed since you opened it. Reload and try again."); }
      else setDialogErr(ae.message);
    } finally { setBusy(false); }
  }
  async function hold() {
    if (!id) return;
    setBusy(true);
    setActionErr("");
    try { setView(await api.hold(id, holdNote)); } catch (e) { setActionErr((e as Error).message); } finally { setBusy(false); }
  }
  async function recheck() {
    if (!id) return;
    setBusy(true);
    try { setView(await api.recheck(id)); setActionErr(""); setFlash("Re-checked against the current terms just now."); setTimeout(() => setFlash(""), 4000); } catch (e) { setActionErr((e as Error).message); } finally { setBusy(false); }
  }

  const locked = bill.status === "approved";
  return (
    <div className="stack">
      <div className="row">
        <h1 style={{ margin: 0 }}><Link to={`/helpers/${helper.id}`}>{helper.name}</Link> · {periodName(bill.period)}</h1>
        <span className={`badge ${bill.status === "approved" ? "good" : bill.status === "held" ? "warn" : "info"}`}>{bill.status === "held" ? "on hold" : bill.status.replace("_", " ")}</span>
        <span className="grow" />
        {bill.check && (bill.check.termsUsed ?? []).length > 0 && <span className="small muted">checked against: {(bill.check.termsUsed ?? []).join("; ").replace(/\d{4}-\d{2}-\d{2}/g, d => dateName(d))}</span>}
      </div>
      <Verdict bill={bill} />
      {actionErr && <div className="error">{actionErr}</div>}

      {!locked && bill.check && (
        <div className="stack" style={{ gap: 6 }}>
          <div className="row">
            <button className="primary" disabled={busy} onClick={() => setApproving(true)}>Approve for payment…</button>
            <input style={{ maxWidth: 260 }} placeholder="Note for the hold (optional)" value={holdNote} onChange={e => setHoldNote(e.target.value)} />
            <button disabled={busy} onClick={hold}>{bill.status === "held" ? "Update hold note" : "Put on hold"}</button>
            <button disabled={busy} onClick={recheck}>Re-check against terms</button>
          </div>
          {flash && <div className="small flash">{flash}</div>}
          <div className="small muted"><strong>Put on hold</strong> marks the bill as waiting (for example while you ask the helper about a line) and keeps the note here; it can still be approved later. <strong>Re-check</strong> re-runs the pricing engine against the terms on record right now, useful after you confirm a rate change. Neither pays anything.</div>
        </div>
      )}
      {!locked && bill.status === "awaiting_terms" && <div className="row"><Link className="btn" to={`/helpers/${helper.id}`}>Confirm {helper.name}'s terms first</Link><button disabled={busy} onClick={recheck}>Re-check against terms</button></div>}

      <div className="split">
        <div className="stack">
          <div className="card stack">
            <strong>Line by line</strong>
            {bill.check ? <BillCheckTable check={bill.check} claimedTotal={bill.bill.claimedTotalCents} /> : (
              <table><thead><tr><th>Date</th><th>What</th><th className="num">Billed</th></tr></thead><tbody>
                {(bill.bill.lines ?? []).map((l, i) => <tr key={i}><td>{l.date}</td><td>{l.note || l.service}</td><td className="num">{money(l.claimedCents)}</td></tr>)}
              </tbody></table>
            )}
            {(bill.check?.flags ?? []).length ? <ul className="plain small">{(bill.check?.flags ?? []).map((f, i) => <li key={i}>{f.level === "problem" ? "⚠︎" : "ℹ︎"} {f.message}</li>)}</ul> : null}
            {bill.assumptions?.length ? <div className="small"><strong>How it was read</strong><ul className="plain">{bill.assumptions.map((a, i) => <li key={i}>{a}</li>)}</ul></div> : null}
          </div>
          {bill.check && (bill.check.pendingDecisions ?? []).length > 0 && !locked && (
            <div className="card stack">
              <strong>Needs your call before paying</strong>
              {(bill.check.pendingDecisions ?? []).map(d => { const lr = bill.check!.lines[d.lineIndex]; return <div className="pending small" key={d.lineIndex}><strong>{lr ? `${dateName(lr.line.date) || ""} ${lineWhat(lr.line) || lr.line.note || ""} · ${money(lr.line.claimedCents)}`.trim() : d.claim}</strong><div>{d.why}</div></div>; })}
            </div>
          )}
          {locked && bill.approval && (
            <div className="card stack">
              <div className="locked">{bill.approval.note ? `Note: “${bill.approval.note}”.` : "Locked."}
                {(bill.approval.excludedPending ?? []).length ? ` Left out: ${(bill.approval.excludedPending ?? []).map(i => bill.bill.lines[i]?.note || bill.bill.lines[i]?.service).join("; ")}.` : ""}
                {(bill.approval.includedPending ?? []).length ? ` Paid as billed despite the flag: ${(bill.approval.includedPending ?? []).map(i => bill.bill.lines[i]?.note || bill.bill.lines[i]?.service).join("; ")}.` : ""}
                {bill.approval.pendingAmounts && Object.keys(bill.approval.pendingAmounts).length ? ` Chosen readings: ${Object.entries(bill.approval.pendingAmounts).map(([i, c]) => `${bill.bill.lines[Number(i)]?.note || bill.bill.lines[Number(i)]?.service} → ${money(c as number)}`).join("; ")}.` : ""}
              </div>
              <div><strong className="small">Message for {helper.name}</strong>
                <textarea readOnly value={message} />
                <button onClick={() => { navigator.clipboard?.writeText(message); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? "Copied" : "Copy message"}</button>
              </div>
            </div>
          )}
        </div>
        <div className="stack">
          <div className="card stack"><strong>The bill as sent</strong><SourceList sources={sources} /></div>
        </div>
      </div>
      {approving && bill.check && <ApproveDialog bill={bill} helper={helper} busy={busy} error={dialogErr} onClose={() => setApproving(false)} onApprove={approve} />}
    </div>
  );
}
