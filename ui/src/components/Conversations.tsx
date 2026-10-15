import { useEffect, useState } from "react";
import { api } from "../api";
import type { SessionSummary } from "../types";

/** Past conversations: open one, or delete it (its unconfirmed cards are rejected; records stay). */
export function Conversations({ current, refreshKey, onOpen, onDeleted }: { current: string; refreshKey: number; onOpen: (id: string) => void; onDeleted: (id: string) => void }) {
  const [items, setItems] = useState<SessionSummary[]>([]);
  const [confirmId, setConfirmId] = useState("");
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const load = () => api.sessions().then(setItems).catch(() => {});
  useEffect(() => { load(); }, [refreshKey, current]);

  async function remove(id: string) {
    setBusy(id); setErr("");
    try {
      const r = await api.deleteSession(id);
      setConfirmId(""); await load(); onDeleted(id);
      if (!r.ok) setErr("The messages are gone, but what the assistant remembered from this conversation could not all be cleared. Try deleting again in a minute.");
    }
    catch (e: any) { setErr(e.message); }
    finally { setBusy(""); }
  }

  const others = items.filter(s => s.id !== current || s.turns > 0);
  if (!others.length) return <div className="muted small">No conversations yet.</div>;
  return (
    <div className="stack history">
      {err && <div className="error small">{err}</div>}
      <div className="stack" style={{ gap: 2 }}>
        {others.map(s => (
          <div key={s.id} className={`history-item ${s.id === current ? "current" : ""}`}>
            <button className="convo" onClick={() => onOpen(s.id)} title={s.title}>
              <div className="title">{s.title}</div>
              <div className="small muted">{new Date(s.lastAt).toLocaleDateString(undefined, { month: "short", day: "numeric" })} · {s.turns} message{s.turns === 1 ? "" : "s"}{s.id === current ? " · open" : ""}</div>
            </button>
            {confirmId === s.id ? (
              <span className="row" style={{ gap: 4, flexWrap: "nowrap" }}>
                <button className="danger small" disabled={busy === s.id} onClick={() => remove(s.id)}>{busy === s.id ? "…" : "Delete"}</button>
                <button className="small" onClick={() => setConfirmId("")}>Keep</button>
              </span>
            ) : (
              <button className="link small muted" title="Delete this conversation" onClick={() => setConfirmId(s.id)}>Delete</button>
            )}
          </div>
        ))}
      </div>
      <div className="small muted">Deleting a conversation removes its messages and what the assistant remembered from it, and rejects any card from it you never confirmed. Confirmed terms and bills stay.</div>
    </div>
  );
}
