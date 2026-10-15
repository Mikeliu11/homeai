import { useEffect, useState } from "react";
import { parseMoney } from "../money";

/** A dollar field that keeps what you type and commits cents on blur/enter. */
export function MoneyInput({ cents, onChange, style }: { cents: number; onChange: (cents: number) => void; style?: React.CSSProperties }) {
  const [text, setText] = useState((cents / 100).toFixed(2));
  useEffect(() => { setText((cents / 100).toFixed(2)); }, [cents]);
  const commit = () => { const v = parseMoney(text); if (v !== null) onChange(v); else setText((cents / 100).toFixed(2)); };
  return <input style={style} value={text} onChange={e => setText(e.target.value)} onBlur={commit} onKeyDown={e => { if (e.key === "Enter") commit(); }} inputMode="decimal" />;
}

export function TextInput({ value, onChange, placeholder, style }: { value: string; onChange: (v: string) => void; placeholder?: string; style?: React.CSSProperties }) {
  const [text, setText] = useState(value);
  useEffect(() => { setText(value); }, [value]);
  return <input style={style} value={text} placeholder={placeholder} onChange={e => setText(e.target.value)} onBlur={() => onChange(text)} onKeyDown={e => { if (e.key === "Enter") onChange(text); }} />;
}
