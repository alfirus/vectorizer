#!/usr/bin/env node
// http entry — hosted use (Docker on ns539881).
// StreamableHTTP + bearer token. Listens on 127.0.0.1 by default;
// reach it via Tailscale (never expose publicly).
//
// Env:
//   MCP_HTTP_PORT   (default 8093)
//   MCP_HTTP_HOST   (default 127.0.0.1)
//   MCP_BEARER_TOKEN (required — refuse to start without it)
//   VECTORIZER_URL / VECTORIZER_API_KEY (same as stdio)
//   VECTORIZER_AGENT_NAME / AGENT_NAME (default X-Agent when caller sends none)
//   MCP_REQUIRE_X_AGENT=true (reject POST /mcp without caller X-Agent —
//     unattributed callers get 400 unless this bridge has a default above)
//
// Callers can send X-Agent on the MCP POST to attribute Vectorizer usage
// to themselves — surfaced as "Daily Usage By Agent" in the dashboard.
import express from "express";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createServer } from "./server.js";
import { parseConfig } from "./config.js";

const PORT = Number(process.env.MCP_HTTP_PORT ?? 8093);
const HOST = process.env.MCP_HTTP_HOST ?? "127.0.0.1";
const TOKEN = process.env.MCP_BEARER_TOKEN ?? "";

if (!TOKEN) {
  console.error("FATAL: MCP_BEARER_TOKEN is required — refusing to start unauthenticated.");
  process.exit(1);
}

const app = express();
app.use(express.json({ limit: "4mb" }));

// Health (no auth — loadbalancer/dokcer probing only, loopback anyway).
app.get("/health", (_req, res) => res.json({ ok: true, service: "vectorizer-mcp" }));

// Bearer gate for everything else.
app.use("/mcp", (req, res, next) => {
  const got = (req.headers.authorization ?? "").replace(/^Bearer\s+/i, "");
  if (!got || got !== TOKEN) {
    res.status(401).json({ error: "unauthorized" });
    return;
  }
  next();
});

app.post("/mcp", async (req, res) => {
  // Stateless: fresh server+transport per request, no session tracking.
  // Correct for a single trusted client; avoids cross-request session store.
  // Forward the caller's X-Agent (if any) so Vectorizer usage attribution
  // follows the real agent, not just the shared bridge.
  const agentName = typeof req.headers["x-agent"] === "string" ? req.headers["x-agent"] : undefined;
  // Compulsory attribution (MCP_REQUIRE_X_AGENT=true): refuse unattributed
  // callers unless this bridge itself carries a default (VECTORIZER_AGENT_NAME).
  // Default OFF so existing clients keep working until they send X-Agent.
  if (process.env.MCP_REQUIRE_X_AGENT === "true" && !agentName?.trim() && !parseConfig().agentName) {
    res.status(400).json({ error: "missing X-Agent header (agent identity is compulsory)" });
    return;
  }
  const server = createServer({ agentName });
  try {
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: undefined,
    });
    await server.connect(transport);
    await transport.handleRequest(req, res, req.body);
  } catch (err) {
    console.error("MCP request error:", err);
    if (!res.headersSent) res.status(500).json({ error: "mcp request failed" });
  }
});

app.get("/mcp", async (req, res) => {
  res.status(405).json({ error: "SSE streams require session — POST first" });
});

app.listen(PORT, HOST, () => {
  console.log(`vectorizer-mcp http on http://${HOST}:${PORT}/mcp`);
});
