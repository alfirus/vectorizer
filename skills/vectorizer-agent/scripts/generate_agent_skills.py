#!/usr/bin/env python3
"""Stamp per-agent Vectorizer memory skills from templates/agent-skill.md + roles.md.

Usage:
  python generate_agent_skills.py --agent sofia --out /path/to/sofia/skills/note-taking/vectorizer/SKILL.md
  python generate_agent_skills.py --agent sofia --skills-root /path/to/profiles
  python generate_agent_skills.py --all --skills-root /path/to/profiles
  python generate_agent_skills.py --all --skills-root /path/to/profiles --ensure-workspace

--ensure-workspace additionally creates the agent's Vectorizer workspace when it
is missing (POST /api/v1/workspaces) and verifies it exists. Workspace name = agent name.
"""
import argparse
import json
import os
import re
import sys
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_TEMPLATE = os.path.join(HERE, "..", "templates", "agent-skill.md")
DEFAULT_ROLES = os.path.join(HERE, "..", "roles.md")
SKILL_REL_PATH = os.path.join("skills", "note-taking", "vectorizer", "SKILL.md")

ROLE_RE = re.compile(r"^##\s+(\S+)\s+—\s+(.+?)\s*$")


def parse_roles(path):
    """Return {agent: (role_title, bullets_block)} from roles.md."""
    roles = {}
    agent = None
    title = None
    buf = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            m = ROLE_RE.match(line)
            if m:
                if agent:
                    roles[agent] = (title, "\n".join(buf).strip())
                agent, title, buf = m.group(1), m.group(2), []
            elif agent is not None:
                if line.startswith("## "):  # any other heading ends the block
                    roles[agent] = (title, "\n".join(buf).strip())
                    agent, title, buf = None, None, []
                else:
                    buf.append(line.rstrip("\n"))
    if agent:
        roles[agent] = (title, "\n".join(buf).strip())
    return roles


def render(template, agent, role_title, bullets):
    return (
        template.replace("{{agent_name}}", agent)
        .replace("{{workspace}}", agent)  # workspace = agent name (enforced convention)
        .replace("{{role_title}}", role_title)
        .replace("{{role_bullets}}", bullets)
    )


def ensure_workspace(agent, api_base, api_key):
    req = urllib.request.Request(
        api_base + "/workspaces", headers={"X-API-Key": api_key, "X-Agent": agent}
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        ids = [w["id"] for w in json.load(r)["workspaces"]]
    if agent in ids:
        return "exists"
    data = json.dumps({"name": agent}).encode()
    req = urllib.request.Request(
        api_base + "/workspaces",
        data=data,
        headers={"X-API-Key": api_key, "X-Agent": agent, "Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        json.load(r)
    with urllib.request.urlopen(
        urllib.request.Request(api_base + "/workspaces", headers={"X-API-Key": api_key}),
        timeout=15,
    ) as r:
        ids = [w["id"] for w in json.load(r)["workspaces"]]
    return "created" if agent in ids else "FAILED"


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--agent", help="single agent name (must match a roles.md block)")
    ap.add_argument("--all", action="store_true", help="stamp every role block in roles.md")
    ap.add_argument("--out", help="explicit output file (single agent only)")
    ap.add_argument("--skills-root", help="write to <root>/<agent>/" + SKILL_REL_PATH)
    ap.add_argument("--roles", default=DEFAULT_ROLES)
    ap.add_argument("--template", default=DEFAULT_TEMPLATE)
    ap.add_argument("--ensure-workspace", action="store_true",
                    help="create the Vectorizer workspace if missing and verify it")
    ap.add_argument("--api-base", default="http://100.90.123.105:8091/api/v1")
    ap.add_argument("--api-key", default="vectorizer-local-key")
    args = ap.parse_args()

    if not args.agent and not args.all:
        ap.error("use --agent <name> or --all")
    if args.out and not args.agent:
        ap.error("--out requires --agent")
    if not args.out and not args.skills_root:
        ap.error("need --out or --skills-root")

    with open(args.template, encoding="utf-8") as f:
        template = f.read()
    roles = parse_roles(args.roles)

    agents = sorted(roles) if args.all else [args.agent]
    for agent in agents:
        if agent not in roles:
            sys.exit(f"no role block for '{agent}' in {args.roles} (add '## {agent} — <Role Title>')")
        role_title, bullets = roles[agent]
        out = args.out or os.path.join(args.skills_root, agent, SKILL_REL_PATH)
        os.makedirs(os.path.dirname(out), exist_ok=True)
        with open(out, "w", encoding="utf-8", newline="\n") as f:
            f.write(render(template, agent, role_title, bullets))
        ws = "n/a"
        if args.ensure_workspace:
            ws = ensure_workspace(agent, args.api_base, args.api_key)
        print(f"{agent:10s} -> {out}  (workspace: {ws})")


if __name__ == "__main__":
    main()
