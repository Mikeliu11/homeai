export function money(cents: number | undefined | null): string {
  if (cents === undefined || cents === null) return "—";
  const neg = cents < 0;
  const abs = Math.abs(cents);
  const whole = Math.floor(abs / 100);
  const frac = abs % 100;
  const s = "$" + whole.toLocaleString("en-US") + (frac ? "." + String(frac).padStart(2, "0") : "");
  return neg ? "-" + s : s;
}

export function parseMoney(s: string): number | null {
  const m = s.replace(/[$,\s]/g, "");
  if (!/^-?\d+(\.\d{1,2})?$/.test(m)) return null;
  return Math.round(parseFloat(m) * 100);
}

export function periodName(p: string): string {
  const [y, m] = p.split("-").map(Number);
  if (!y || !m) return p;
  return new Date(y, m - 1, 1).toLocaleString("en-US", { month: "long", year: "numeric" });
}

export function dateName(d?: string): string {
  if (!d) return "";
  const [y, m, day] = d.split("-").map(Number);
  if (!y) return d;
  return new Date(y, m - 1, day).toLocaleString("en-US", { weekday: "short", month: "short", day: "numeric" });
}

export function clock(c?: string): string {
  if (!c) return "";
  const [h, m] = c.split(":").map(Number);
  if (h === 24) return "12am";
  const suffix = h >= 12 ? "pm" : "am";
  const hh = h % 12 === 0 ? 12 : h % 12;
  return m ? `${hh}:${String(m).padStart(2, "0")}${suffix}` : `${hh}${suffix}`;
}
