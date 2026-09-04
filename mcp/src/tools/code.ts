import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json, err } from "../types.js";

export function registerCodeTools(server: McpServer, getClient: () => Client) {
  server.tool("vectorizer_index_repo", "Index a SERVER-LOCAL repo path into a code_<repo> workspace: per-symbol chunks + DEFINES/CALLS/IMPORTS reasoning edges. Unchanged files skipped by hash. Path must exist on the Vectorizer server (e.g. /opt/vectorizer), not the agent's laptop.", {
    path: z.string().min(1), workspace_id: z.string().optional(), session_id: z.string().optional(),
  }, async (a) => {
    try { const data = await getClient().req("/api/v1/code/index", { method: "POST", body: JSON.stringify(a) }); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_symbols", "List indexed symbols per file in a code workspace (optional file substring filter).", {
    workspace_id: z.string().min(1), file: z.string().optional(),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id });
      if (a.file) q.set("file", a.file);
      const data = await getClient().req(`/api/v1/code/symbols?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_callers", "Structural 'what calls X?' over CALLS reasoning edges in a code workspace.", {
    workspace_id: z.string().min(1), symbol: z.string().min(1),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id, symbol: a.symbol });
      const data = await getClient().req(`/api/v1/code/callers?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
}
