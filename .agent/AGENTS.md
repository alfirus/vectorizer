# Project AI Development Instructions

## Source of Truth

The `.agent/` directory contains the project-specific instructions for AI coding agents.

All AI coding agents and IDE integrations must follow these instructions.

## General Rules

- Inspect `.agent/` before making changes.
- Follow the most specific applicable instruction.
- Preserve the existing project architecture.
- Reuse existing components and patterns.
- Maintain the existing visual design system.
- Avoid unnecessary dependencies.
- Avoid unrelated refactoring.
- Preserve existing functionality.
- Follow project security requirements.
- Validate changes before considering a task complete.

## Design Consistency

New UI must match the existing application's:

- Typography
- Colors
- Spacing
- Layout
- Components
- Borders
- Radius
- Shadows
- Icons
- Interaction patterns
- Responsive behavior

Do not introduce an unrelated visual style.

## Architecture

Follow the existing project architecture unless there is a documented reason to change it.

Before creating a new abstraction, inspect the existing codebase for an equivalent pattern.

Vectorizer architecture (see `docs/BLUEPRINT.md` v0.2.0):
- Go/Fiber REST on `:8091` + gRPC on `:50051`
- ChromaDB v2 `ws_<id>` collections (768d `nomic-embed-text`, cosine HNSW)
- Layered config: `env > .env > config.toml > defaults` (`config/config.go`)
- Store handles chunking (4000 chars), embedding, hybrid search (BM25 RRF)
- Handlers: workspaces, sessions, peers, messages, conclusions, webhooks, brain, admin
- Additional layers: `internal/security` (JWT w/p/ad), `internal/dreamer` (768d), `mcp/` proxy

## Code Quality

Prefer:

- Simple implementations
- Reusable components
- Existing utilities
- Existing dependencies
- Strong typing where applicable
- Minimal changes
- Maintainable code

Avoid unnecessary abstraction and duplication.

## Validation

After making changes, run the appropriate project validation:

- `go vet ./...`
- `go build .`
- `npm run build --prefix mcp` (when MCP changes)
- `npm run build --prefix sdks/typescript` (when SDK changes)
- Other project-specific checks

## Continuous Improvement

When a new project-wide convention is discovered or explicitly established, update the appropriate `.agent/` instruction file so future AI agents follow the same rule.

Do not silently change established project rules.

## Agent Compatibility

These instructions are intended to be followed regardless of whether the project is opened using:

- VS Code
- Zed
- Antigravity
- CLI coding agents
- Other AI development environments
