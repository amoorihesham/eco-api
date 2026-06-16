---
name: new-execution-plan
description: Create, scaffold, and validate an eco-api execution plan (docs/executions/<PHASE>-<slug>.md). Use when asked to write/start/draft an execution plan for a phase (P4, P5, …), turn an IMPLEMENTATION_PLAN phase into an execution doc, or check that an execution plan conforms to the house template.
---

# Authoring an eco-api execution plan

An **execution plan** expands one phase from [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md)
(P0–P18) into a detailed, paste-ready implementation doc under [docs/executions/](docs/executions/).
Every plan follows the **same 12-section template** ([P3](docs/executions/P3-identity-auth.md) is the
fullest reference). This skill's driver scaffolds a new plan seeded from the phase entry, and validates
that any plan conforms.

The driver is [.claude/skills/new-execution-plan/plan.mjs](.claude/skills/new-execution-plan/plan.mjs)
(Node, no dependencies). **Paths below are relative to the repo root** (`d:\eco-api`).

## Workflow (agent path)

**1. Scaffold** from the phase's entry in `IMPLEMENTATION_PLAN.md` (Goal → Objective, Scope → In-scope,
Depends on, and the DoD sentence are all pre-filled; the rest is `TODO`):

```bash
node .claude/skills/new-execution-plan/plan.mjs new P4
```

The slug is derived from the phase title; override it by passing one:

```bash
node .claude/skills/new-execution-plan/plan.mjs new P5 seller-onboarding-store
```

This writes `docs/executions/P5-seller-onboarding-store.md` and refuses to overwrite an existing file.

**2. Fill in every `TODO`.** Read the seeded §1 Overview, then write §2–§12 by mining the source docs:
- [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) — this phase's Scope/Contracts/Risks (the spec you're expanding).
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — the rule §6 should restate (module template, outbox, table-ownership, ownership checks).
- [docs/PRD.md](docs/PRD.md) and [api/openapi.yaml](api/openapi.yaml) — the FRs and endpoints §1/§9 must realize.
- The **previous** plan's "§12 Handoff" section — it names the seams this phase plugs into.
- Real code under [internal/](internal/) — §8 code blocks must match existing conventions (see the repo [CLAUDE.md](CLAUDE.md)).

**3. Validate.** A green check is the structural half of the plan's Definition of Done:

```bash
node .claude/skills/new-execution-plan/plan.mjs check docs/executions/P4-account.md
```

Check every plan at once (use this before committing):

```bash
node .claude/skills/new-execution-plan/plan.mjs check
```

`check` exits non-zero if any plan is missing a section, has them out of order, lacks the metadata
table / `In scope` / `Out of scope` / `### S1` / `**Check:**` lines / a `- [ ]` DoD checklist / a
```` ```powershell ```` verification block, or still contains `TODO` markers.

## The 12-section template (what `check` enforces)

1. Overview — Objective, **In scope**, **Out of scope**, Depends on
2. Prerequisites (Windows / PowerShell)
3. Tech stack & versions
4. Target file tree (**delta on the previous phase**, not the whole repo)
5. Execution steps — `### S1`, `### S2`, … each ending in a **`Check:`** line
6. The phase-specific **contract / convention** (the one design rule this phase establishes)
7. Configuration reference (additions to the previous phase)
8. Full file contents — complete, paste-ready
9. Testing plan
10. Definition of Done — a `- [ ]` checklist
11. Verification (PowerShell)
12. Handoff to the next phase

Plus a top metadata table (**Phase, Status, Date, Outcome, Module path**) and companion links to
`../PRD.md` and `../ARCHITECTURE.md`.

## Gotchas

- **`new` seeds, it does not finish.** A fresh scaffold has ~29 `TODO`s and intentionally **fails**
  `check` until you resolve them — that failure is the to-do list, not a bug.
- **§4 is a delta.** List only files this phase adds or changes (`delta on P<n-1>`), never the full tree.
- **§6 is phase-specific** — `check` only requires *a* section 6 heading, so name it for the actual
  rule (e.g. "The auth & module-template contract", "The transactional-outbox & idempotency contract").
- **Status starts as `Draft — not ready to implement`.** Flip it to `Ready to implement` only once the
  plan is complete and `check` is green.
- **Node is required** to run the driver (`node --version` → v24 here). It has zero npm dependencies.

## Troubleshooting

- `error: phase P4 not found in docs/IMPLEMENTATION_PLAN.md` — the phase id must match a `## P<n> — …`
  heading in that file. Valid ids are P0–P18.
- `error: <path> already exists — refusing to overwrite` — pick a new slug, or edit/delete the existing draft.
- `FAIL … N unresolved "TODO" marker(s)` — expected for a new scaffold; resolve them and re-run `check`.
