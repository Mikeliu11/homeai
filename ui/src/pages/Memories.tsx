import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { MemoryRecord } from "../types";

/** What the assistant remembers across conversations (AgentCore Memory). Context, never agreements. */
export default function Memories() {
  const [items, setItems] = useState<MemoryRecord[] | null>(null);
  const [err, setErr] = useState("");
  const [confirmClear, setConfirmClear] = useState(false);
  const [clearing, setClearing] = useState(false);
  const load = () => { setErr(""); api.memory().then(setItems).catch(e => setErr(String(e.message || e))); };
  useEffect(() => { load(); }, []);
  const resetAt = (() => { try { return Number(localStorage.getItem("hp.resetAt") || 0); } catch { return 0; } })();
  const justReset = resetAt > 0 && Date.now() - resetAt < 5 * 60 * 1000;
  return (
    <div className="stack">
      <div className="row">
        <h1 style={{ margin: 0 }}>Memories</h1>
        <span className="grow" />
        <button onClick={load}>Refresh</button>
        {confirmClear ? (
          <span className="row" style={{ gap: 6 }}>
            <button className="danger" disabled={clearing} onClick={async () => { setClearing(true); setErr(""); try { const r = await api.clearMemory(); if (!r.ok) setErr("Some memories could not be cleared. Try again in a minute."); try { localStorage.setItem("hp.resetAt", String(Date.now())); } catch {} await load(); } catch (e: any) { setErr(e.message); } finally { setClearing(false); setConfirmClear(false); } }}>{clearing ? "Clearing…" : "Yes, forget everything"}</button>
            <button onClick={() => setConfirmClear(false)}>Keep</button>
          </span>
        ) : <button className="danger" onClick={() => setConfirmClear(true)}>Clear memories</button>}
      </div>
      <p className="muted">Household context the assistant picked up from past conversations: who is who, how each helper is paid, schedule changes. These are recollections, not agreements; clearing them changes no record. Rates and bills live under <Link to="/helpers">Helpers</Link> and <Link to="/bills">Bills</Link>, and only count once you confirm them.</p>
      {err && <div className="error">{err}</div>}
      {items === null && !err && <p className="muted">Loading…</p>}
      {items && items.length === 0 && <div className="card empty"><p><strong>Nothing remembered yet.</strong></p><p>Facts you mention on <Link to="/home">Home</Link> get picked up over the next minute or so.</p></div>}
      {items && items.length > 0 && justReset && <div className="card small muted">You started over a moment ago. These facts were deleted; the memory service takes a minute or two to stop listing them.</div>}
      {items && items.length > 0 && (
        <div className="card">
          <ul className="plain">{items.map(m => <li key={m.id} style={{ padding: "6px 0" }}>{m.text}</li>)}</ul>
        </div>
      )}
    </div>
  );
}
