import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api";
import { invalidateHelpers } from "../components/useHelpers";
import { Conversations } from "../components/Conversations";

/** Start over: deletes every record, upload and memory, as if freshly deployed. */
export default function Settings() {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [problems, setProblems] = useState<string[]>([]);
  const [showHistory, setShowHistory] = useState(false);
  const nav = useNavigate();
  const currentSession = (() => { try { return localStorage.getItem("hp.session") || ""; } catch { return ""; } })();

  async function reset() {
    setBusy(true); setErr(""); setProblems([]);
    try {
      const r = await api.reset();
      try { localStorage.removeItem("hp.session"); localStorage.setItem("hp.resetAt", String(Date.now())); } catch {}
      invalidateHelpers();
      if (r.ok) nav("/home", { replace: true });
      else setProblems(r.problems ?? []);
    } catch (e: any) {
      setErr(e.code === "turn_in_progress" ? e.message : `${e.message} It is safe to run Start over again.`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <h1>Settings</h1>
      <div className="card stack">
        <div className="row">
          <div><h2 style={{ margin: 0 }}>History</h2><div className="small muted">Past conversations on Home. Open one to continue it, or delete it.</div></div>
          <span className="grow" />
          <button onClick={() => setShowHistory(v => !v)}>{showHistory ? "Hide history" : "Show history"}</button>
        </div>
        {showHistory && (
          <Conversations current={currentSession} refreshKey={0}
            onOpen={id => { try { localStorage.setItem("hp.session", id); } catch {} nav("/home"); }}
            onDeleted={id => { if (id === currentSession) { try { localStorage.removeItem("hp.session"); } catch {} } }} />
        )}
      </div>
      <div className="card stack">
        <h2 style={{ margin: 0 }}>Start over</h2>
        <p>Deletes everything in this household: helpers and their terms, bills and approvals, conversations, uploads, and what the assistant remembers. The app goes back to the state right after deployment. Your login stays.</p>
        <p className="small">Approved bills are locked against edits, not against starting over. One audit entry records that a reset happened.</p>
        <label className="stack" style={{ maxWidth: 320 }}>
          <span className="small">Type <strong>RESET</strong> to confirm</span>
          <input value={typed} onChange={e => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
        </label>
        <div className="row">
          <button className="danger" disabled={typed !== "RESET" || busy} onClick={reset}>{busy ? "Deleting…" : "Delete everything and start over"}</button>
        </div>
        {err && <div className="error">{err}</div>}
        {problems.length > 0 && <div className="error">Partly done: {problems.join(", ")} could not be fully deleted. Run Start over again.</div>}
      </div>
    </div>
  );
}
