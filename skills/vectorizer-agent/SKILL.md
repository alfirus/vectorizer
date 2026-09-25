---
name: vectorizer-agent
description: Provision an agent's Vectorizer memory skill + personal workspace (workspace naming, X-Agent attribution, role workflows)
---
# Vectorizer Agent Provisioning

Give every agent a personal semantic-memory skill backed by its own Vectorizer workspace. Provisioned agents recall/record through the live server (`http://100.90.123.105:8091/api/v1`) instead of losing context every session.

## Conventions (enforced)

- **Workspace = agent name**, lowercase (`sofia`, `maisarah`, `shiela`, …). One per agent, never shared. Create it if missing.
- **`X-Agent: <name>`** on every call — the usage dashboard attributes per-agent activity by it (README "Agent Attribution").
- **`code_<project>`** workspaces are programming-project indexes (per-symbol chunks + DEFINES/CALLS/IMPORTS edges). `code_vectorizer` = this repo. Prose memory never goes in `code_*`.
- Shared workspaces: `_global` (identity facts — seen by every scoped search), `family` (the indexed markdown vault).

## Provision steps

1. **Workspace:** `GET /api/v1/workspaces` → if the agent is missing, `POST /api/v1/workspaces {"name":"<agent>"}`. Verify it appears in the list.
2. **Skill:** stamp the per-agent skill from the template + role workflow:

   ```
   python skills/vectorizer-agent/scripts/generate_agent_skills.py \
     --agent sofia --role "CEO" --out <skills-root>/sofia/skills/note-taking/vectorizer/SKILL.md
   # or provision every role listed in roles.md:
   python skills/vectorizer-agent/scripts/generate_agent_skills.py --all --roles skills/vectorizer-agent/roles.md --skills-root <profiles-root>
   ```

   On Hermes, `<skills-root>/<agent>/skills/note-taking/vectorizer/SKILL.md` is where the agent's skill loader finds it (the `note-taking` category exists on every profile).
3. **MCP bridge:** wire the shared MCP bridge into the agent's Hermes config so `vectorizer_*` tools load in every session:

   ```
   hermes -p <agent> config set mcp_servers.vectorizer.url http://100.90.123.105:8093/mcp
   hermes -p <agent> config set mcp_servers.vectorizer.headers.Authorization "Bearer <MCP_BEARER_TOKEN>"
   hermes -p <agent> config set mcp_servers.vectorizer.headers.X-Agent <agent>
   hermes -p <agent> config set mcp_servers.vectorizer.timeout 180
   hermes -p <agent> config set mcp_servers.vectorizer.connect_timeout 30
   ```

   `<MCP_BEARER_TOKEN>` is the bridge's token (`MCP_BEARER_TOKEN` in the `vectorizer-mcp` container env). The per-request `X-Agent` header is what keeps usage attributed per agent on a shared bridge — never set `VECTORIZER_AGENT_NAME` on the shared bridge itself. Tools register as `mcp_vectorizer_vectorizer_*`; verify with `hermes -p <agent> mcp test vectorizer` (expect 25 tools). MCP servers load at agent start — running agents pick this up on their next restart.

4. **Verify:** the skill file exists, the workspace is listed, and a store→search round-trip works with `X-Agent: <name>`:

   ```
   curl -s -X POST http://100.90.123.105:8091/api/v1/messages \
     -H "X-API-Key: vectorizer-local-key" -H "X-Agent: <name>" -H "Content-Type: application/json" \
     -d '{"workspace_id":"<agent>","session_id":"provision-check","role":"system","content":"provision check","metadata":{"importance":1}}'
   curl -s -X POST http://100.90.123.105:8091/api/v1/messages/search \
     -H "X-API-Key: vectorizer-local-key" -H "X-Agent: <name>" -H "Content-Type: application/json" \
     -d '{"query":"provision check","n_results":1,"where":{"workspace_id":"<agent>"}}'
   ```

## Role workflows

Every agent skill carries a `## Your workflow as <Role>` section — what that role should store, recall, and correct. The current team's blocks live in `roles.md`. When a new teammate joins: add a `## <agent> — <Role Title>` block there (5-7 bullets: what to store with which importance, what to recall before acting, which code/provenance endpoints matter for the role), then run the generator.

## Files

- `templates/agent-skill.md` — the per-agent skill template (`{{agent_name}}`, `{{workspace}}`, `{{role_title}}`, `{{role_bullets}}` placeholders).
- `roles.md` — role workflow bullets for the current team.
- `scripts/generate_agent_skills.py` — stamps one agent (`--agent`) or all roles (`--all`).

## Pitfalls

- Workspace names are exact and case-sensitive downstream (`ws_<name>` collections) — always lowercase.
- Never stamp a second workspace for the same agent (`<agent>2`, `<agent>_v2`) — re-use the one workspace; search recency and the reasoning graph live there.
- The skill hardcodes the live endpoint and workspace — re-stamp it when either changes; don't hand-edit 18 copies.
