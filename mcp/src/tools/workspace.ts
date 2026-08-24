import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json, err } from "../types.js";

export function registerWorkspaceTools(server: McpServer, getClient: () => Client) {
  server.tool("vectorizer_list_workspaces", "List workspaces (ChromaDB collections ws_*)", {}, async () => {
    try { const data = await getClient().req("/api/v1/workspaces"); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_create_workspace", "Create workspace (ensures ws_<id> collection, 768d)", { name: z.string().min(1) }, async (a) => {
    try { const data = await getClient().req("/api/v1/workspaces", { method: "POST", body: JSON.stringify({ name: a.name }) }); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_get_workspace_stats", "Get workspace document count", { workspace_id: z.string().min(1) }, async (a) => {
    try { const data = await getClient().req(`/api/v1/workspaces/${encodeURIComponent(a.workspace_id)}/stats`); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_health", "Health + ChromaDB status", {}, async () => {
    try { const data = await getClient().req("/api/v1/health"); return json(data); } catch (e) { return err(e); }
  });
}
