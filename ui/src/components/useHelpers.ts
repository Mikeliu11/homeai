import { useEffect, useState } from "react";
import { api } from "../api";
import type { Helper } from "../types";

let cache: Record<string, Helper> | null = null;
let inflight: Promise<Record<string, Helper>> | null = null;

export function invalidateHelpers() { cache = null; inflight = null; }

/** id → helper, shared across cards so a page with ten cards makes one request. */
export function useHelpers(): Record<string, Helper> {
  const [map, setMap] = useState<Record<string, Helper>>(cache ?? {});
  useEffect(() => {
    if (cache) { setMap(cache); return; }
    inflight ??= api.helpers().then(hs => { cache = Object.fromEntries(hs.map(h => [h.id, h])); return cache; }).catch(() => ({}));
    inflight.then(setMap);
  }, []);
  return map;
}
