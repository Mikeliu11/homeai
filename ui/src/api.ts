import { config } from "./config";
import type { AuditEvent, Bill, BillView, Helper, MemoryRecord, Proposal, SessionSummary, Source, TermsVersion, Turn } from "./types";

let tokenProvider: () => Promise<string | null> = async () => null;
export function setTokenProvider(p: () => Promise<string | null>) { tokenProvider = p; }

export class ApiError extends Error {
  code: string; status: number; body: any;
  constructor(status: number, body: any) {
    super(body?.error || `request failed (${status})`);
    this.status = status; this.code = body?.code || "error"; this.body = body;
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const token = await tokenProvider();
  if (token) headers.Authorization = token;
  let res: Response;
  try {
    res = await fetch(config().apiUrl + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, { error: "Can't reach the server. Check your connection and try again.", code: "network" });
  }
  const text = await res.text();
  let json: any = null;
  try { json = text ? JSON.parse(text) : null; } catch { json = { error: text }; }
  if (!res.ok) {
    if (res.status >= 500 && !(json && json.error && /try again|went wrong/.test(json.error))) json = { error: "The server had a problem. Try again in a moment.", code: "server" };
    if (res.status === 401 || res.status === 403) json = { error: "Your session has expired. Reload and sign in again.", code: "unauthorized" };
    throw new ApiError(res.status, json);
  }
  return json as T;
}

export const api = {
  helpers: () => call<Helper[]>("GET", "/helpers"),
  helper: (id: string) => call<{ helper: Helper; versions: TermsVersion[]; bills: Bill[]; proposals: Proposal[]; sources: Source[]; audit: AuditEvent[] }>("GET", `/helpers/${id}`),
  proposals: (status = "open") => call<Proposal[]>("GET", `/proposals?status=${status}`),
  proposal: (id: string) => call<Proposal>("GET", `/proposals/${id}`),
  confirm: (id: string, hash: string, edits?: unknown) => call<{ kind: string; termsVersion?: TermsVersion; bill?: Bill }>("POST", `/proposals/${id}/confirm`, { hash, edits }),
  reject: (id: string) => call<{ status: string }>("POST", `/proposals/${id}/reject`),
  bills: (helperId?: string) => call<Bill[]>("GET", `/bills${helperId ? `?helperId=${helperId}` : ""}`),
  bill: (id: string) => call<BillView>("GET", `/bills/${id}`),
  approve: (id: string, body: { hash: string; amountCents?: number; method: string; note?: string; includedPending: number[]; excludedPending: number[]; pendingAmounts?: Record<number, number> }) => call<BillView>("POST", `/bills/${id}/approve`, body),
  hold: (id: string, note: string) => call<BillView>("POST", `/bills/${id}/hold`, { note }),
  recheck: (id: string) => call<BillView>("POST", `/bills/${id}/recheck`),
  createTurn: (body: { sessionId: string; text: string; sourceIds: string[]; clientKey: string }) => call<Turn>("POST", "/turns", body),
  turn: (id: string) => call<{ turn: Turn; proposals: Proposal[] }>("GET", `/turns/${id}`),
  turns: (sessionId: string) => call<Turn[]>("GET", `/turns?sessionId=${encodeURIComponent(sessionId)}`),
  upload: (body: { kind: string; contentType?: string; filename?: string; text?: string; senderLabel?: string }) => call<{ source: Source; putUrl?: string }>("POST", "/uploads", body),
  source: (id: string) => call<{ source: Source; getUrl?: string }>("GET", `/sources/${id}`),
  memory: () => call<MemoryRecord[]>("GET", "/memory"),
  reset: () => call<{ ok: boolean; problems?: string[] }>("POST", "/reset", { confirm: "RESET" }),
  sessions: () => call<SessionSummary[]>("GET", "/sessions"),
  clearMemory: () => call<{ ok: boolean }>("POST", "/memory/clear"),
  deleteSession: (id: string) => call<{ ok: boolean; turns: number; rejectedCards: number; problems?: string[] }>("POST", `/sessions/${id}/delete`),
  audit: () => call<AuditEvent[]>("GET", "/audit"),
};

export async function putBlob(url: string, blob: Blob, contentType: string) {
  const res = await fetch(url, { method: "PUT", headers: { "Content-Type": contentType }, body: blob });
  if (!res.ok) throw new Error(`upload failed (${res.status})`);
}
