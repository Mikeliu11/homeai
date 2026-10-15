import { useCallback, useEffect, useRef, useState } from "react";

import { api, putBlob } from "../api";
import type { Proposal, Source, Turn } from "../types";
import { ProposalCard } from "../components/ProposalCard";
import { Markdown } from "../components/Markdown";

type Attachment = { source: Source; preview?: string; name: string };

function newId() { return Math.random().toString(36).slice(2) + Date.now().toString(36); }

// The model accepts PNG/JPEG/WebP/GIF up to about 3.75 MB with no side over 8000 px.
// Anything else (HEIC, a huge photo, a tall stitched screenshot) is redrawn as JPEG,
// keeping as many pixels as allowed so the text stays readable.
async function toUploadable(file: File): Promise<{ blob: Blob; type: string }> {
  const ok = ["image/png", "image/jpeg", "image/webp", "image/gif"];
  const bmp = await createImageBitmap(file).catch(() => null);
  if (!bmp) {
    if (ok.includes(file.type) && file.size < 3_500_000) return { blob: file, type: file.type };
    throw new Error(`Can't read ${file.name}. Use PNG or JPEG.`);
  }
  const side = Math.max(bmp.width, bmp.height);
  if (ok.includes(file.type) && file.size < 3_500_000 && side <= 7500) return { blob: file, type: file.type };
  const maxSide = file.size < 3_500_000 ? 7500 : 2400;
  const scale = Math.min(1, maxSide / side);
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bmp.width * scale); canvas.height = Math.round(bmp.height * scale);
  canvas.getContext("2d")!.drawImage(bmp, 0, 0, canvas.width, canvas.height);
  let blob = await new Promise<Blob | null>(r => canvas.toBlob(r, "image/jpeg", 0.9));
  if (blob && blob.size >= 3_500_000) blob = await new Promise<Blob | null>(r => canvas.toBlob(r, "image/jpeg", 0.7));
  if (!blob) throw new Error("Could not convert the image");
  return { blob, type: "image/jpeg" };
}

// keepAsSource mirrors send(): text that looks like a message, note or bill is
// stored as a source of its own, so one of the turn's sourceIds is the text itself.
function keepAsSource(body: string) { return body.length > 60 || /\n|\$|\d/.test(body); }
function attachmentCount(t: Turn) { return (t.sourceIds?.length ?? 0) - (t.userText && keepAsSource(t.userText) ? 1 : 0); }

export default function Home() {
  const [sessionId, setSessionId] = useState(() => { try { return localStorage.getItem("hp.session") || newId(); } catch { return newId(); } });
  const [turns, setTurns] = useState<Turn[]>([]);
  const [proposals, setProposals] = useState<Record<string, Proposal>>({});
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(0);
  const [err, setErr] = useState("");
  const [over, setOver] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => { try { localStorage.setItem("hp.session", sessionId); } catch {} }, [sessionId]);

  const loadProposals = useCallback(async (ids: string[]) => {
    const missing = ids.filter(i => !proposals[i]);
    if (!missing.length) return;
    const got = await Promise.all(missing.map(i => api.proposal(i).catch(() => null)));
    setProposals(p => { const n = { ...p }; got.forEach(g => { if (g) n[g.id] = g; }); return n; });
  }, [proposals]);

  useEffect(() => {
    api.turns(sessionId).then(ts => { setTurns(ts); loadProposals(ts.flatMap(t => t.proposalIds ?? [])); }).catch(e => setErr(e.message));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // poll unfinished turns; after repeated failures, say so instead of spinning forever
  const failures = useRef<Record<string, number>>({});
  useEffect(() => {
    const open = turns.filter(t => t.status === "queued" || t.status === "running");
    if (!open.length) return;
    const h = setInterval(async () => {
      for (const t of open) {
        try {
          const r = await api.turn(t.id);
          failures.current[t.id] = 0;
          setTurns(ts => ts.map(x => (x.id === r.turn.id ? { ...r.turn, userText: x.userText || r.turn.userText } : x)));
          if (r.proposals.length) setProposals(p => ({ ...p, ...Object.fromEntries(r.proposals.map(x => [x.id, x])) }));
        } catch (e) {
          failures.current[t.id] = (failures.current[t.id] ?? 0) + 1;
          if (failures.current[t.id] >= 6) {
            setTurns(ts => ts.map(x => (x.id === t.id ? { ...x, status: "failed", error: `Lost contact while waiting for the assistant (${(e as Error).message}). Reload to check whether it finished.` } : x)));
          }
        }
      }
    }, 1500);
    return () => clearInterval(h);
  }, [turns]);

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: "smooth" }); }, [turns.length]);

  async function addFiles(files: FileList | File[]) {
    setErr("");
    setUploading(n => n + Array.from(files).length);
    for (const f of Array.from(files)) {
      try {
        const { blob, type } = await toUploadable(f);
        const { source, putUrl } = await api.upload({ kind: "image", contentType: type, filename: f.name });
        if (putUrl) await putBlob(putUrl, blob, type);
        setAttachments(a => [...a, { source, preview: URL.createObjectURL(blob), name: f.name }]);
      } catch (e) { setErr((e as Error).message); }
      finally { setUploading(n => Math.max(0, n - 1)); }
    }
  }

  async function send() {
    if (busy || uploading > 0) return; // Enter must not send while a screenshot is still uploading
    const body = text.trim();
    if (!body && !attachments.length) return;
    setBusy(true); setErr("");
    try {
      const sourceIds = attachments.map(a => a.source.id);
      // anything that looks like a message, note or bill is kept as a source for provenance;
      // the text is sent too so the conversation shows Jordan's own words
      if (keepAsSource(body)) {
        const { source } = await api.upload({ kind: "text", text: body });
        sourceIds.unshift(source.id);
      }
      const t = await api.createTurn({ sessionId, text: body, sourceIds, clientKey: newId() });
      setTurns(ts => [...ts, { ...t, userText: body, sourceIds }]);
      setText(""); setAttachments([]);
    } catch (e) { setErr((e as Error).message); } finally { setBusy(false); }
  }

  function onProposalDone(id: string) {
    api.proposal(id).then(p => setProposals(ps => ({ ...ps, [id]: p }))).catch(() => {});
  }

  return (
    <div className="chat">
      <div className="stack">
        <div className="row">
          <h1 style={{ margin: 0 }}>Home</h1>
          <span className="grow" />
          {turns.length > 0 && <button onClick={() => { setSessionId(newId()); setTurns([]); setProposals({}); }}>New conversation</button>}
        </div>
        <div className="messages">
          {turns.length === 0 && (
            <div className="card empty">
              <p><strong>Start by pasting what you have about a helper.</strong></p>
              <p className="small">A text thread, a screenshot of one, or notes from memory. Then paste the month's bill the same way. Everything the assistant drafts shows up as a card you confirm; nothing is recorded until you do.</p>
            </div>
          )}
          {turns.map(t => (
            <div key={t.id} className="stack">
              {(t.userText || t.sourceIds?.length) ? <div className="msg user">{t.userText || "(attachment)"}{attachmentCount(t) > 0 ? <div className="attach">📎 {attachmentCount(t)} attachment{attachmentCount(t) > 1 ? "s" : ""}</div> : null}</div> : null}
              {t.status === "queued" || t.status === "running" ? <div className="msg system">… {t.progress || "thinking"}</div> : null}
              {t.status === "failed" ? <div className="msg assistant error">{t.error || "Something went wrong."}</div> : null}
              {t.status === "done" && <div className="msg assistant"><Markdown text={t.reply ?? ""} /></div>}
              {t.proposalIds?.map(pid => proposals[pid] ? <ProposalCard key={pid} proposal={proposals[pid]} onDone={() => onProposalDone(pid)} /> : null)}
            </div>
          ))}
          <div ref={bottomRef} />
        </div>
        <div className="card stack" onDragOver={e => { e.preventDefault(); setOver(true); }} onDragLeave={() => setOver(false)} onDrop={e => { e.preventDefault(); setOver(false); addFiles(e.dataTransfer.files); }}>
          <textarea value={text} onChange={e => setText(e.target.value)} placeholder="Paste a text thread, an email, or type what you remember… (Enter to send, Shift+Enter for a new line)"
            onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }} />
          {attachments.length > 0 && <div className="row">{attachments.map(a => <span key={a.source.id} className="row small">{a.preview && <img className="thumb" src={a.preview} alt={a.name} />}{a.name}<button className="link" aria-label={`Remove ${a.name}`} onClick={() => setAttachments(x => x.filter(y => y.source.id !== a.source.id))}>✕</button></span>)}</div>}
          <div className="row">
            <div className={`dropzone grow ${over ? "over" : ""}`} role="button" tabIndex={0} onClick={() => fileRef.current?.click()} onKeyDown={e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); fileRef.current?.click(); } }}>{uploading > 0 ? `Uploading ${uploading} screenshot${uploading > 1 ? "s" : ""}…` : "Drop screenshots here or click to choose (PNG or JPEG)"}</div>
            <input ref={fileRef} type="file" accept="image/*" multiple hidden onChange={e => e.target.files && addFiles(e.target.files)} />
            <button className="primary" disabled={busy || uploading > 0 || (!text.trim() && !attachments.length)} onClick={send}>{busy ? "Sending…" : uploading > 0 ? "Uploading…" : "Send"}</button>
          </div>
          {err && <div className="error small">{err}</div>}
        </div>
      </div>
    </div>
  );
}
