import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json } from "../types.js";

function fail(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e);
  return json({ ok: false, error: msg });
}

function dashBase(getClient: () => Client): string {
  const fromCfg = getClient().cfg.dashboardUrl ?? "";
  if (fromCfg) return fromCfg.replace(/\/$/, "");
  return (process.env.DASHBOARD_URL ?? "").replace(/\/$/, "");
}

async function dashGet(getClient: () => Client, path: string): Promise<unknown> {
  const res = await fetch(`${dashBase(getClient)}${path}`, {
    headers: { "Content-Type": "application/json" },
  });
  const text = await res.text();
  let body: unknown = null;
  try { body = text ? JSON.parse(text) : null; } catch { body = text; }
  if (!res.ok) {
    const msg = typeof body === "object" && body !== null && "error" in (body as Record<string, unknown>)
      ? String((body as Record<string, unknown>).error) : text || res.statusText;
    throw new Error(`${res.status} ${msg}`);
  }
  return body;
}

export function registerVaultTools(server: McpServer, getClient: () => Client) {
  server.tool(
    "vault_list",
    "List vault files (paginated MEMORY_INDEX.json entries via dashboard)",
    {
      offset: z.number().int().min(0).optional(),
      limit: z.number().int().min(1).max(200).optional(),
      workspace: z.string().min(1).optional(),
    },
    async (a) => {
      try {
        if (!dashBase(getClient)) return json({ ok: false, error: "DASHBOARD_URL not configured" });
        const offset = a.offset ?? 0;
        const limit = a.limit ?? 50;
        const data = await dashGet(getClient,
          `/api/vault?action=list&offset=${offset}&limit=${limit}`
        ) as { ok?: boolean; total?: number; files?: Array<Record<string, unknown>>; [k: string]: unknown };
        // NOTE: dashboard /api/vault has no `workspace` param (only `q`);
        // filter client-side by path substring so the tool arg still works.
        if (a.workspace && Array.isArray(data.files)) {
          const w = a.workspace.toLowerCase();
          const files = data.files.filter((f) => String(f.path ?? "").toLowerCase().includes(w));
          return json({ ...data, ok: true, workspace: a.workspace, files });
        }
        return json({ ok: true, ...(typeof data === "object" && data !== null ? data : { data }) });
      } catch (e) { return fail(e); }
    }
  );

  server.tool(
    "vault_read",
    "Read one vault file's content (via dashboard; server enforces vault-root traversal guard)",
    { path: z.string().min(1) },
    async (a) => {
      try {
        if (!dashBase(getClient)) return json({ ok: false, error: "DASHBOARD_URL not configured" });
        const data = await dashGet(getClient, `/api/vault?action=file&path=${encodeURIComponent(a.path)}`);
        return json({ ok: true, ...(typeof data === "object" && data !== null ? (data as Record<string, unknown>) : { data }) });
      } catch (e) { return fail(e); }
    }
  );
}
