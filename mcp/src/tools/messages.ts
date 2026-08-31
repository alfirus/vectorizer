import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json, err } from "../types.js";

export function registerMessageTools(server: McpServer, getClient: () => Client) {
  server.tool("vectorizer_add_message", "Store message (chunks to 4000, embeds 768d, upserts to ws_<workspace_id>)", {
    workspace_id: z.string().optional(), session_id: z.string().min(1),
    role: z.enum(["user","assistant","system"]), content: z.string().min(1),
  }, async (a) => {
    try {
      const wid = (a as Record<string,string>).workspace_id ?? getClient().cfg.workspaceId ?? "maisarah";
      const data = await getClient().req("/api/v1/messages", { method: "POST", body: JSON.stringify({ ...a, workspace_id: wid }) }); return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_add_messages_batch", "Batch store messages", {
    workspace_id: z.string().optional(), messages: z.array(z.object({
      workspace_id: z.string().optional(), session_id: z.string().min(1), role: z.string().min(1), content: z.string().min(1),
    })).min(1),
  }, async (a) => {
    try { const data = await getClient().req("/api/v1/messages/batch", { method: "POST", body: JSON.stringify(a) }); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_list_messages", "List messages by workspace/session (via ChromaDB get)", {
    workspace_id: z.string().min(1), session_id: z.string().optional(), limit: z.number().int().min(1).max(100).default(20), offset: z.number().int().min(0).default(0),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id, limit: String(a.limit), offset: String(a.offset) });
      if (a.session_id) q.set("session_id", a.session_id);
      const data = await getClient().req(`/api/v1/messages?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_search", "Semantic search (cosine, HNSW) with optional session/role + pagination", {
    query: z.string().min(1), workspace_id: z.string().optional(), session_id: z.string().optional(),
    role: z.string().optional(), n_results: z.number().int().min(1).max(100).default(5),
  }, async (a) => {
    try {
      const where: Record<string,string> = {};
      const wid = a.workspace_id ?? getClient().cfg.workspaceId ?? "maisarah";
      where.workspace_id = wid;
      if (a.session_id) where.session_id = a.session_id;
      if (a.role) where.role = a.role;
      const data = await getClient().req("/api/v1/messages/search", { method: "POST", body: JSON.stringify({ query: a.query, n_results: a.n_results, where }) });
      return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_search_all", "Search across ALL workspaces in parallel (semantic + keyword via RRF)", {
    query: z.string().min(1), n_results: z.number().int().min(1).max(100).default(5),
  }, async (a) => {
    try {
      const data = await getClient().req("/api/v1/messages/search/all", { method: "POST", body: JSON.stringify({ query: a.query, n_results: a.n_results }) });
      return json(data);
    } catch (e) { return err(e); }
  });
}
