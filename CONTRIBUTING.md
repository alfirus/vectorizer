# Contributing

This repository follows the org Architecture Decision Record (ADR) standard. The rule below is a merge gate: an architecturally significant PR is blocked until its ADR has been merged.

## Architecture Decision Records — Engineering Repo Contributing Rule

### ADR Requirement

Any architecturally significant decision must be recorded as an ADR in `docs/adr/` **before** implementation begins. This is a gate, not optional.

#### What triggers an ADR

Decisions involving any of the following require an ADR:
- Technology stack choice (language, framework, database)
- Schema or data model change
- Authentication / authorization model
- Third-party service integration
- Cross-service contract or API design
- Infrastructure deployment pattern
- Security architecture change

### Workflow

1. Draft the ADR in `docs/adr/NNNN-title.md` with Status = **Proposed**.
2. Share it with the squad for review (Lisa, Elizabeth, Alya, Kira, Balqis).
3. Mirza or Maisarah reviews and changes status to **Accepted**.
4. The ADR PR is merged before any implementation work starts.

### Implementation linkage

The implementing kanban card body and the implementation PR must reference the accepted ADR ID (e.g., `ADR 0002`). This creates a traceable link from decision to code.

### Referencing

See `docs/adr/README.md` for full conventions: numbering, naming, superseding rules, and who may accept decisions.
