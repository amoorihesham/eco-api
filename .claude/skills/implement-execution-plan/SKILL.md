---
name: implement-execution-plan
description: Implement an eco-api execution plan (docs/executions/<PHASE>-<slug>.md) end to end — work the §5 steps in order, type in the §8 file contents, run each step's Check, then satisfy §9 tests / §10 DoD / §11 verification. Use when asked to "implement P4", "build the next phase", "execute the plan", or "do the P5 plan".
---

# Implementing an eco-api execution plan

An **execution plan** under [docs/executions/](docs/executions/) is a detailed, paste-ready spec for
one phase (P0–P18). This skill turns that doc into working, tested code by walking its
**§5 execution steps** in order, using its **§8 full file contents** as the source of truth, and
gating each step on the **`Check:`** line the plan gives. The companion authoring skill is
`new-execution-plan` (it writes/validates the doc); this skill *implements* a doc that's already
`Ready to implement`.

**Paths below are relative to the repo root** (`d:\eco-api`). Commands run via `task` (Windows /
PowerShell). The architecture rules you must not break live in [CLAUDE.md](CLAUDE.md) and
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Before you start

**1. Pick the plan.** If the user named a phase (`P4`), the file is `docs/executions/P4-*.md`. Read the
**whole plan** before writing any code — §5 (steps) and §8 (file contents) are the contract, but §1
(scope), §4 (file-tree delta), §6 (this phase's convention), and §10 (DoD) tell you what "done" means.

**2. Refuse to start a plan that isn't ready.** Check the top metadata table:
- **Status must be `Ready to implement`.** If it's `Draft — not ready to implement`, stop and tell the
  user to finish the plan first (`new-execution-plan` validates it). Don't implement a draft.
- Run `node .claude/skills/new-execution-plan/plan.mjs check docs/executions/<file>.md` — a green check
  means the doc is structurally complete (no leftover `TODO`s). If it fails, surface that; the plan
  author needs to fix it, not you.

**3. Confirm the starting point.** §2 (Prerequisites) lists what must already be true — usually the
previous phase's migration version. Verify it (`task migrate:version`, `task db:up`) before step S1, or
you'll fail the first Check for the wrong reason.

## Workflow — drive §5 top to bottom

Work the steps **in the plan's order** (S1, S2, …); they're sequenced so each compiles on the last.
Use [TodoWrite] to track the steps so the user can see progress on a multi-step phase.

For **each step `Sn`**:

1. **Do what the step says**, taking code **verbatim from §8** ("Full file contents") and SQL from the
   migration/queries blocks. §8 is paste-ready and authoritative — don't improvise an alternative
   design. Adjust only obvious typos/names, and match the surrounding code's idioms.
2. **Honor the file-tree delta (§4).** Create/modify exactly the files it marks `NEW`/`CHANGED`; don't
   touch files it says are unchanged (e.g. P4 explicitly leaves `cmd/api/main.go` alone). If you find
   you need a file §4 doesn't list, stop and flag the mismatch rather than inventing scope.
3. **Run the step's `Check:` line** (a `go build ./…`, `task migrate:version`, `task sqlc`, etc.) and
   confirm it passes before moving on. A failing Check is a stop sign — fix it, don't push past it.
4. For codegen steps: after editing `migrations/` + `queries/*.sql`, run `task generate` (or `task sqlc`)
   and **commit the generated output** — `task sqlc:check` must stay clean.

When all steps are done, close out the plan:

- **§9 Testing plan / §10 DoD** — run `task test`, then `task test:integration` (needs Postgres up), then
  `task ci` for the full pipeline (tidy → generate → lint → test → build). Walk the §10 `- [ ]` checklist
  item by item and confirm each holds; report any you can't satisfy rather than glossing over it.
- **§11 Verification** — run the PowerShell block (boot with `task run`, exercise the endpoints) when the
  user wants the live demo, or hand them the block to run. Use the `verify` / `run` skills for driving the
  app if helpful.
- **Flip the status.** Once the DoD holds, update the plan's metadata table: `Status` changes from
  `Ready to implement` to `Implemented` — literally that value, not a paraphrase — and fill the
  `Date`/`Outcome` if the plan leaves them open. Do this for every phase you implement; don't skip
  it just because earlier phase docs in the repo were left unflipped. This is a doc edit, not code.
- **Offer to commit** via the `commit` skill — one logical commit, phase-tagged
  (e.g. `MODULE: P4 account profile & addresses implemented`). Don't commit unless asked; mention a
  feature branch if on `main` for a substantial phase.

## Hard rules (from CLAUDE.md — review enforces these)

The plan's §8 already respects these, but verify as you type code in — a paste-and-rename can drift:

- **`domain/` and `service/` import no infrastructure SDK** — no `pgx`, `net/http`, `stripe-go`. Only
  `repo/` and `platform/*` adapters may. (`service` *may* name `pgx.Tx`/`db.Beginner` as the
  unit-of-work currency — the allow-listed exception.)
- **Cross-module access goes through a sibling's `port.go` only.** Never import another module's
  `service`/`repo`/`domain`; never read its tables; no cross-module FK or join. Cross-module references
  store a plain UUID, resolved via ports (sync reads) or events (async).
- **Tables are prefixed by the owning module** (`identity_users`, `order_orders`).
- **Money and quantities are integer minor units** (cents), never floats.
- **The transactional outbox is atomic.** A state change and its event emission happen inside one
  `db.RunInTx`: the service writes its rows *and* `outbox.Write(ctx, tx, evt)` on the **same `tx`**.
  Repo **write** methods take a `pgx.Tx`; **read** methods take only `ctx` and return `pgx.ErrNoRows`
  (the service maps that to a domain error). Keep this convention for any new repo method.
- **Wiring is explicit in [cmd/api/main.go](cmd/api/main.go).** A new module means: build adapters →
  repo → service → handler there, mount routes in `newRouter`, and register event subscribers on the
  `bus` **before** the dispatcher goroutine starts. (Many plans that *extend* a module — like P4 — change
  no wiring; trust §4 on whether `main.go` is touched.)

## Rules

- **The plan is the spec — don't redesign it mid-implementation.** If a step is wrong, unbuildable, or
  contradicts the code, **stop and tell the user**; propose a plan edit. Don't silently substitute a
  different approach, and don't expand scope beyond §1/§4.
- **One step at a time, Check-gated.** Don't batch all of §8 into existence and hope it builds. The
  per-step Checks exist so a failure is localized.
- **Don't skip the generated-code commit.** Stale `identitydb`/`dbgen`/`eventsdb` output fails
  `task sqlc:check` in CI.
- **Integration tests need a real Postgres** (`task db:up`); they self-skip when `DATABASE_URL` is unset,
  so a green `task test` alone does **not** prove the DB-backed invariants. Run `task test:integration`
  before claiming the DoD.
- **Report failures honestly.** If a Check, test, or DoD item fails, say so with the output — a phase
  that "compiles but the integration test is red" is not done.

## Gotchas

- **§8 is the source of truth, §5 is the order.** §5 often summarizes ("full in §8"); always pull the
  actual code from §8, not from the one-line description in the step.
- **§4 is a delta on the previous phase**, not the whole repo — it's the precise list of files in play.
  A file not in §4 is out of scope for this phase.
- **§6 is the one design rule this phase establishes** (P4: ownership/tenant-isolation; P2: outbox). Read
  it — it's the convention later phases copy, and the DoD checks you honored it.
- **`task ci` is the final gate**, but it's slow; use the fast `go build ./…` Checks per step and save
  `task ci` for the end of §9/§10.
- **§8 snippets may not satisfy `revive`.** golangci-lint's `revive` rule fails the build if any
  exported func/method/type lacks a doc comment, but the plan's §8 code blocks sometimes omit them
  (prose-formatted, not lint-checked). A per-step `go build ./…` Check won't catch this — only
  `task lint`/`task ci` will. When typing in a §8 snippet that adds exported symbols, add a short
  one-line doc comment over each one as you go, matching the file's existing house style, instead of
  discovering the gap only when the final lint Check fails.
