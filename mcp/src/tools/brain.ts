import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json, err } from "../types.js";

export function registerBrainTools(server: McpServer, getClient: () => Client) {
  server.tool("vectorizer_summarize", "Summarize text or workspace/session messages (LLM brain, opt-in)", {
    text: z.string().optional(), workspace_id: z.string().optional(), session_id: z.string().optional(), max_chars: z.number().int().optional(),
  }, async (a) => {
    try { const data = await getClient().req("/api/v1/brain/summarize", { method: "POST", body: JSON.stringify(a) }); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_ask", "Ask question over memory (RAG: auto-searches workspace if context omitted)", {
    question: z.string().min(1), context: z.string().optional(), workspace_id: z.string().optional(), session_id: z.string().optional(),
  }, async (a) => {
    try { const data = await getClient().req("/api/v1/brain/ask", { method: "POST", body: JSON.stringify(a) }); return json(data); } catch (e) { return err(e); }
  });
  server.tool("vectorizer_trace", "Walk memory provenance: direction=forward answers 'why do I believe X?' (conclusion -> premises -> supporting messages); direction=reverse answers 'what breaks if I forget X?' (blast radius). Bounded BFS, no cycles.", {
    workspace_id: z.string().min(1), id: z.string().min(1),
    direction: z.enum(["forward", "reverse"]).default("forward"), depth: z.number().int().min(1).max(10).default(5),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id, id: a.id, direction: a.direction, depth: String(a.depth) });
      const data = await getClient().req(`/api/v1/conclusions/trace?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_stale", "Dead-knowledge scan: proposes old, never-reinforced, non-timeless memories for review (nothing deleted — confirm via vectorizer_delete_message or TTL).", {
    workspace_id: z.string().min(1),
    max_age_days: z.number().int().min(1).default(90), limit: z.number().int().min(1).max(200).default(50),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id, max_age_days: String(a.max_age_days), limit: String(a.limit) });
      const data = await getClient().req(`/api/v1/conclusions/stale?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
  server.tool("vectorizer_brief", "One-shot session-start overview: stats + representation + recent conclusions + top entities (+ optional stale count) in a single call.", {
    workspace_id: z.string().min(1), peer_id: z.string().optional(),
    max_conclusions: z.number().int().min(1).max(50).default(10), include_stale: z.boolean().default(false),
  }, async (a) => {
    try {
      const q = new URLSearchParams({ workspace_id: a.workspace_id, max_conclusions: String(a.max_conclusions), include_stale: String(a.include_stale) });
      if (a.peer_id) q.set("peer_id", a.peer_id);
      const data = await getClient().req(`/api/v1/conclusions/brief?${q}`); return json(data);
    } catch (e) { return err(e); }
  });
}
