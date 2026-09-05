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
import express from "express";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createServer } from "./server.js";

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
  const server = createServer();
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
