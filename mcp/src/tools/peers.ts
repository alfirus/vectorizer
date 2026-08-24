import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json, err } from "../types.js";
export function registerPeerTools(server: McpServer, getClient: () => Client) {
  server.tool("vectorizer_create_peer", "Create peer in workspace (Honcho peer parity)", { workspace_id: z.string(), peer_id: z.string()}, async (a) => {
    try { const d=await getClient().req(`/api/v1/workspaces/${a.workspace_id}/peers`, {method:"POST", body:JSON.stringify({id:a.peer_id})}); return json(d);} catch(e){return err(e)}
  });
  server.tool("vectorizer_chat", "Dialectic chat (peer-aware RAG + peer_cards, Honcho peer.chat parity)", { workspace_id: z.string(), query: z.string(), observer: z.string().optional(), observed: z.string().optional(), session_id: z.string().optional()}, async (a) => {
    try { const d=await getClient().req(`/api/v1/workspaces/${a.workspace_id}/chat`, {method:"POST", body:JSON.stringify(a)}); return json(d);} catch(e){return err(e)}
  });
  server.tool("vectorizer_upload", "Upload file/document for ingestion (chunked 4000, 768d)", { workspace_id: z.string(), session_id: z.string(), content: z.string()}, async (a) => {
    try { const d=await getClient().req(`/api/v1/messages/upload`, {method:"POST", body:JSON.stringify(a)}); return json(d);} catch(e){return err(e)}
  });
}
