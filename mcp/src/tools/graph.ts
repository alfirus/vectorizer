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

type GraphNode = Record<string, unknown> & { id?: unknown };
type GraphEdge = Record<string, unknown> & { from?: unknown; to?: unknown };

export function registerGraphTools(server: McpServer, getClient: () => Client) {
  server.tool(
    "graph_build_status",
    "Graph build status: node/edge counts + workspace list (not the full graph)",
    {},
    async () => {
      try {
        if (!dashBase(getClient)) return json({ ok: false, error: "DASHBOARD_URL not configured" });
        const g = await dashGet(getClient, `/api/graph`) as {
          nodes?: GraphNode[]; edges?: GraphEdge[]; workspaces?: string[];
          total_nodes?: number; total_edges?: number;
        };
        const nodes = Array.isArray(g.nodes) ? g.nodes.length : (g.total_nodes ?? 0);
        const edges = Array.isArray(g.edges) ? g.edges.length : (g.total_edges ?? 0);
        const workspaces = Array.isArray(g.workspaces)
          ? g.workspaces
          : [...new Set((g.nodes ?? []).map((n) => String((n as Record<string, unknown>).workspace ?? "unknown")))];
        return json({ ok: true, nodes, edges, workspaces });
      } catch (e) { return fail(e); }
    }
  );

  server.tool(
    "graph_nodes",
    "List graph nodes, optionally filtered by workspace (sliced client-side)",
    {
      workspace: z.string().min(1).optional(),
      limit: z.number().int().min(1).max(500).optional(),
    },
    async (a) => {
      try {
        if (!dashBase(getClient)) return json({ ok: false, error: "DASHBOARD_URL not configured" });
        const limit = a.limit ?? 100;
        const qs = a.workspace ? `?workspace=${encodeURIComponent(a.workspace)}` : "";
        const g = await dashGet(getClient, `/api/graph${qs}`) as {
          nodes?: GraphNode[]; edges?: GraphEdge[];
          total_nodes?: number; total_edges?: number; workspace?: string;
        };
        const allNodes = Array.isArray(g.nodes) ? g.nodes : [];
        const allEdges = Array.isArray(g.edges) ? g.edges : [];
        const nodes = allNodes.slice(0, limit);
        const ids = new Set(nodes.map((n) => n.id));
        const edges = allEdges.filter((e) => ids.has(e.from) && ids.has(e.to));
        return json({
          ok: true,
          workspace: a.workspace ?? g.workspace ?? null,
          total_nodes: g.total_nodes ?? allNodes.length,
          total_edges: g.total_edges ?? allEdges.length,
          returned_nodes: nodes.length,
          returned_edges: edges.length,
          nodes,
          edges,
        });
      } catch (e) { return fail(e); }
    }
  );
}
