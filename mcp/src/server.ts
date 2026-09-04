import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { parseConfig, createClient } from "./config.js";
import { registerWorkspaceTools } from "./tools/workspace.js";
import { registerMessageTools } from "./tools/messages.js";
import { registerBrainTools } from "./tools/brain.js";
import { registerPeerTools } from "./tools/peers.js";
import { registerCodeTools } from "./tools/code.js";

export function createServer() {
  const server = new McpServer({ name: "@vectorizer/mcp", version: "0.2.0" });
  const cfg = parseConfig();
  const getClient = () => createClient(cfg);
  registerWorkspaceTools(server, getClient);
  registerMessageTools(server, getClient);
  registerBrainTools(server, getClient);
  registerPeerTools(server, getClient);
  registerCodeTools(server, getClient);
  return server;
}
