export type AppConfig = {
  apiUrl: string;
  region?: string;
  userPoolId?: string;
  userPoolClientId?: string;
  local?: boolean;
};

let cached: AppConfig | null = null;

export async function loadConfig(): Promise<AppConfig> {
  if (cached) return cached;
  const res = await fetch("/config.json", { cache: "no-store" });
  if (!res.ok) throw new Error("config.json missing: the app was not deployed correctly");
  cached = (await res.json()) as AppConfig;
  return cached;
}

export function config(): AppConfig {
  if (!cached) throw new Error("config not loaded");
  return cached;
}
