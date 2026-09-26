# docs/adr/ — Architecture Decision Records

## Purpose
This directory is the org's single source of truth for architecturally significant decisions. It replaces all overlapping decision-log mechanisms (Log4brains, DECISIONS.md, free-text decision notes).

## Format
All ADRs follow the MADR-style template in `TEMPLATE.md`:
- **Title** — short descriptive name
- **Status** — Proposed → Accepted → Deprecated or Superseded
- **Context** — background and alternatives
- **Decision** — immutable once accepted
- **Consequences** — trade-offs, risks, assumptions

## Naming & Numbering

- Files are named `NNNN-title.md` where NNNN is a sequential four-digit number.
- Numbers increment globally across all engineering repos that adopt this standard — never restart at `0001` in a new repo.
- One decision per ADR. Do not combine unrelated decisions into one record.
- Title uses kebab-case (e.g., `0002-choice-of-postgres.md`).

### Current allocation (org registry)

| ADR | Repo | File | Status |
| --- | --- | --- | --- |
| 0001 | `alfirus/theglobe` | `docs/adr/0001-use-sveltekit-routes-as-the-bridge.md` | Accepted |

0001 is the SvelteKit-routes-as-bridge decision and stays where it is — it is not renumbered and not reissued. **The next ADR written anywhere in the org is 0002.** A number is reserved by the open ADR PR, not by a local edit: put the number in the PR title when you claim it, so two repos never take the same one.

## Who May Accept

Only **Mirza** (Tech Lead) or **Maisarah** (Engineering Manager) may change an ADR's status from Proposed to Accepted. No other team member has this authority.

## Superseding

When a later decision invalidates an earlier one:
1. Create a new ADR with the next sequential number.
2. Set its Status to **Superseded** (or **Deprecated** if the old choice is no longer in use).
3. In the new ADR's References section, link the superseded ADR by ID (e.g., `ADR 0001`).
4. The superseded ADR itself remains immutable — only its Status field changes.

## Workflow

Any architecturally significant decision must be recorded as an ADR **before** implementation begins:

1. **Draft** the ADR in Proposed status.
2. **Review** with the squad (Lisa, Elizabeth, Alya, Kira, Balqis).
3. **Accept** by Mirza or Maisarah — status changes to Accepted.
4. **Implement** — the implementing kanban card and PR must reference the ADR ID in their body/metadata.

This gate is enforced by the ADR rule in this repo's `CONTRIBUTING.md` and by the `## ADR` field in the PR template.

## Architecturally Significant Decisions (ASD)

An ADR is required for decisions that include any of:
- Technology stack choice (language, framework, database)
- Schema or data model change
- Authentication / authorization model
- Third-party service integration
- Cross-service contract or API design
- Infrastructure deployment pattern
- Security architecture change

## Indexing

Ain indexes `docs/adr/` in the org `DOCS.md`. Shiela adds an ADR field to engineering PR templates. Elizabeth's kanban card context-footer and Maisarah's handoff notes reference ADR IDs — no free-text decision logs anywhere else.
