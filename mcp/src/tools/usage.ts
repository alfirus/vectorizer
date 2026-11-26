import { z } from "zod";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { Client } from "../config.js";
import { json } from "../types.js";

function fail(e: unknown) {
  const msg = e instanceof Error ? e.message : String(e);
  return json({ ok: false, error: msg });
}

const ACTION_KEYS = ["search", "store", "ask", "chat", "code", "upload", "other"];

type DayRow = {
  date: string;
  total?: number;
  by_agent?: Record<string, number>;
  by_source?: Record<string, number>;
  by_agent_action?: Record<string, Record<string, number>>;
  [k: string]: unknown;
};

export function registerUsageTools(server: McpServer, getClient: () => Client) {
  server.tool(
    "usage_summary",
    "Daily usage summary from vectorizer (proxied /api/v1/usage/daily payload as-is)",
    { days: z.number().int().min(1).max(90).optional() },
    async (a) => {
      try {
        const days = a.days ?? 30;
        const data = await getClient().req(`/api/v1/usage/daily?days=${days}`);
        return json(data);
      } catch (e) { return fail(e); }
    }
  );

  server.tool(
    "usage_by_agent",
    "Daily usage aggregated by agent (prefers by_agent, falls back to by_source; per-agent action mix from by_agent_action)",
    { days: z.number().int().min(1).max(90).optional() },
    async (a) => {
      try {
        const days = a.days ?? 7;
        const payload = await getClient().req(`/api/v1/usage/daily?days=${days}`) as { days?: DayRow[] };
        const daysData: DayRow[] = Array.isArray(payload.days) ? payload.days : [];

        const agentTotals: Record<string, number> = {};
        const agentActions: Record<string, Record<string, number>> = {};
        const daily: Record<string, Record<string, number>> = {};

        const addActions = (agent: string, mix: unknown) => {
          if (!mix || typeof mix !== "object") return;
          for (const k of ACTION_KEYS) {
            const n = Number((mix as Record<string, unknown>)[k]) || 0;
            if (n > 0) agentActions[agent][k] = (agentActions[agent][k] || 0) + n;
          }
        };

        for (const d of daysData) {
          daily[d.date] = {};
          const byAgent = (d.by_agent && Object.keys(d.by_agent).length ? d.by_agent : d.by_source) || {};
          for (const [agent, count] of Object.entries(byAgent)) {
            const n = Number(count) || 0;
            if (n > 0) {
              agentTotals[agent] = (agentTotals[agent] || 0) + n;
              daily[d.date][agent] = n;
              if (!agentActions[agent]) agentActions[agent] = {};
              addActions(agent, d.by_agent_action?.[agent]);
            }
          }
          // Agents present only in by_agent_action (defensive, mirrors dashboard)
          if (d.by_agent_action && typeof d.by_agent_action === "object") {
            for (const agent of Object.keys(d.by_agent_action)) {
              if (!(agent in agentTotals)) {
                if (!agentActions[agent]) agentActions[agent] = {};
                addActions(agent, d.by_agent_action[agent]);
                const t = ACTION_KEYS.reduce((s, k) => s + (agentActions[agent][k] || 0), 0);
                if (t > 0) agentTotals[agent] = t;
              }
            }
          }
        }

        const agents = Object.entries(agentTotals)
          .sort((x, y) => y[1] - x[1])
          .map(([name, total]) => ({ name, total, actions: agentActions[name] || {} }));
        const totalCalls = Object.values(agentTotals).reduce((s, v) => s + v, 0);
        return json({ ok: true, days, totalCalls, agents, daily });
      } catch (e) { return fail(e); }
    }
  );
}
