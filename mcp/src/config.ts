export type VectorizerConfig = {
  baseUrl: string;
  apiKey: string;
  workspaceId?: string;
};

export function parseConfig(): VectorizerConfig {
  const baseUrl = (process.env.VECTORIZER_URL ?? process.env.VECTORIZER_BASE_URL ?? "http://localhost:8091").replace(/\/$/, "");
  const apiKey = process.env.VECTORIZER_API_KEY ?? process.env.DEFAULT_API_KEY ?? "";
  const workspaceId = process.env.VECTORIZER_WORKSPACE_ID ?? process.env.WORKSPACE_ID;
  return { baseUrl, apiKey, workspaceId };
}

export function createClient(cfg: VectorizerConfig) {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (cfg.apiKey) headers["X-API-Key"] = cfg.apiKey;
  async function req(path: string, init?: RequestInit) {
    const res = await fetch(`${cfg.baseUrl}${path}`, {
      ...init,
      headers: { ...headers, ...(init?.headers as Record<string,string> ?? {}) },
    });
    const text = await res.text();
    let json: unknown = null;
    try { json = text ? JSON.parse(text) : null; } catch { json = text; }
    if (!res.ok) {
      const msg = typeof json === "object" && json !== null && "error" in (json as Record<string,unknown>)
        ? String((json as Record<string,unknown>).error) : text || res.statusText;
      throw new Error(`${res.status} ${msg}`);
    }
    return json;
  }
  return { req, cfg };
}
export type Client = ReturnType<typeof createClient>;
