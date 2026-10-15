import type { ReactNode } from "react";

/** Just enough markdown for assistant replies: **bold**, "- " bullets, paragraphs. Nothing else. */
function inline(text: string, key: number): ReactNode {
  const parts = text.split(/(\*\*[^*]+\*\*|\*[^*\n]+\*)/g);
  return <span key={key}>{parts.map((p, i) =>
    p.startsWith("**") && p.endsWith("**") && p.length > 4 ? <strong key={i}>{p.slice(2, -2)}</strong> :
    p.startsWith("*") && p.endsWith("*") && p.length > 2 ? <em key={i}>{p.slice(1, -1)}</em> : p)}</span>;
}

export function Markdown({ text }: { text: string }) {
  const blocks: ReactNode[] = [];
  let list: string[] = [];
  const flush = (k: number) => { if (list.length) { blocks.push(<ul className="plain" key={"l" + k}>{list.map((l, i) => <li key={i}>{inline(l, i)}</li>)}</ul>); list = []; } };
  const lines = text.replace(/^#+\s*/gm, "").split("\n");
  lines.forEach((raw, i) => {
    const line = raw.trimEnd();
    const m = line.match(/^\s*(?:[-*•]|\d+[.)])\s+(.*)$/);
    if (m) { list.push(m[1]); return; }
    flush(i);
    if (line.trim() === "") return;
    blocks.push(<p key={i} style={{ margin: "0 0 6px" }}>{inline(line, i)}</p>);
  });
  flush(lines.length);
  return <div>{blocks}</div>;
}
