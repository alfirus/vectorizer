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
}
