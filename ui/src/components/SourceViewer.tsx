import { useEffect, useState } from "react";
import { api } from "../api";
import type { Source } from "../types";

export function SourceViewer({ source }: { source: Source }) {
  const [url, setUrl] = useState<string | undefined>();
  useEffect(() => {
    if (source.s3Key) api.source(source.id).then(r => setUrl(r.getUrl)).catch(() => {});
  }, [source.id, source.s3Key]);
  return (
    <div className="source stack">
      <div className="small muted">
        {source.kind === "image" ? "Screenshot" : source.kind === "note" ? "Note" : "Pasted text"}
        {source.senderLabel ? ` · ${source.senderLabel}` : ""} · uploaded {new Date(source.uploadedAt).toLocaleDateString()}
      </div>
      {source.text ? <pre>{source.text}</pre> : url ? <img src={url} alt="source screenshot" /> : <div className="muted small">loading…</div>}
    </div>
  );
}

export function SourceList({ sources }: { sources: Source[] }) {
  if (!sources.length) return <p className="muted small">No sources attached.</p>;
  return <div className="stack">{sources.map(s => <SourceViewer key={s.id} source={s} />)}</div>;
}
