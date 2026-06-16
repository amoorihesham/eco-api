#!/usr/bin/env node
// plan.mjs — author & validate eco-api execution plans (docs/executions/<PHASE>-<slug>.md).
//
//   node .claude/skills/new-execution-plan/plan.mjs new   P4 [account-profile-addresses]
//   node .claude/skills/new-execution-plan/plan.mjs check docs/executions/P4-account-profile-addresses.md
//   node .claude/skills/new-execution-plan/plan.mjs check               # checks every plan in docs/executions/
//
// `new`   scaffolds a skeleton seeded from the phase entry in docs/IMPLEMENTATION_PLAN.md,
//         so the structure + metadata + Goal/Scope/DoD are already filled in and you write the body.
// `check` enforces the 12-section template every plan in docs/executions/ follows. Run it after
//         editing — a green check is the structural half of the plan's Definition of Done.

import { readFileSync, writeFileSync, existsSync, readdirSync } from "node:fs";
import { join, basename } from "node:path";

const REPO = process.cwd();
const PLAN = join(REPO, "docs", "IMPLEMENTATION_PLAN.md");
const EXEC_DIR = join(REPO, "docs", "executions");

// The 12 sections every execution plan carries, in order. Section 6 is phase-specific
// (a contract/convention), so we only require *a* section 6 heading, not a fixed title.
const SECTIONS = [
  { n: 1, title: "Overview", re: /^##\s+1\.\s+Overview/m },
  { n: 2, title: "Prerequisites", re: /^##\s+2\.\s+Prerequisites/m },
  { n: 3, title: "Tech stack & versions", re: /^##\s+3\.\s+Tech stack/m },
  { n: 4, title: "Target file tree", re: /^##\s+4\.\s+Target file tree/m },
  { n: 5, title: "Execution steps", re: /^##\s+5\.\s+Execution steps/m },
  { n: 6, title: "Contract / convention (phase-specific)", re: /^##\s+6\.\s+\S/m },
  { n: 7, title: "Configuration reference", re: /^##\s+7\.\s+Configuration reference/m },
  { n: 8, title: "Full file contents", re: /^##\s+8\.\s+Full file contents/m },
  { n: 9, title: "Testing plan", re: /^##\s+9\.\s+Testing plan/m },
  { n: 10, title: "Definition of Done", re: /^##\s+10\.\s+Definition of Done/m },
  { n: 11, title: "Verification", re: /^##\s+11\.\s+Verification/m },
  { n: 12, title: "Handoff", re: /^##\s+12\.\s+Handoff/m },
];

// ---- shared: pull one phase's entry out of IMPLEMENTATION_PLAN.md ----------

function loadPhase(phaseId) {
  if (!existsSync(PLAN)) die(`cannot find ${PLAN}`);
  const md = readFileSync(PLAN, "utf8");
  // Heading looks like:  ## P4 — Account: Profile & Addresses
  const re = new RegExp(`^##\\s+${phaseId}\\s+[—-]\\s+(.+)$`, "m");
  const m = md.match(re);
  if (!m) die(`phase ${phaseId} not found in docs/IMPLEMENTATION_PLAN.md`);
  const title = m[1].trim();
  const start = m.index + m[0].length;
  const rest = md.slice(start);
  const next = rest.search(/^##\s+P\d+\b/m);
  const block = next === -1 ? rest : rest.slice(0, next);

  const field = (label) => {
    // grab "- **Label:** ...." possibly spanning indented sub-bullets until the next "- **"
    const fre = new RegExp(`-\\s+\\*\\*${label}[:\\*]`, "m");
    const fm = block.match(fre);
    if (!fm) return "";
    const after = block.slice(fm.index);
    const stop = after.slice(3).search(/^-\s+\*\*/m); // next top-level field
    const chunk = stop === -1 ? after : after.slice(0, stop + 3);
    return chunk.trim();
  };

  return {
    id: phaseId,
    title, // e.g. "Account: Profile & Addresses"
    goal: field("Goal"),
    scope: field("Scope"),
    dependsOn: field("Depends on"),
    contracts: field("Contracts & events"),
    realizes: field("Realizes"),
    dod: field("Definition of Done"),
    risks: field("Risks / pitfalls"),
  };
}

function slugify(s) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

// ---- `new`: scaffold ------------------------------------------------------

function scaffold(phaseId, slugArg) {
  const p = loadPhase(phaseId);
  const num = Number(phaseId.replace(/\D/g, ""));
  const prev = `P${num - 1}`;
  const next = `P${num + 1}`;
  const slug = slugArg || slugify(p.title.split(/[:—-]/)[0]);
  const out = join(EXEC_DIR, `${phaseId}-${slug}.md`);
  if (existsSync(out)) die(`${out} already exists — refusing to overwrite`);

  const today = new Date().toISOString().slice(0, 10);
  const bullets = (block, fallback) => {
    if (!block) return fallback;
    // turn the IMPLEMENTATION_PLAN field into seed text, stripping the "- **Label:**" prefix
    return block.replace(/^-\s+\*\*[^*]+\*\*:?\s*/, "").trim() || fallback;
  };

  const md = `# Execution Plan — ${phaseId}: ${p.title}

| | |
|---|---|
| **Phase** | ${phaseId} — ${p.title} (see [../IMPLEMENTATION_PLAN.md](../IMPLEMENTATION_PLAN.md)) |
| **Status** | Draft — not ready to implement |
| **Date** | ${today} |
| **Outcome** | TODO: one sentence — the demoable end state of this phase. |
| **Module path** | \`eco-api\` |

> This is an **execution document**: detailed enough to implement directly. Code blocks are working
> skeletons — type them in, adjust names to taste. Companion docs: [PRD](../PRD.md) ·
> [ARCHITECTURE](../ARCHITECTURE.md) · [OpenAPI](../../api/openapi.yaml).

---

## 1. Overview

**Objective.** ${bullets(p.goal, "TODO")}

**In scope**
- TODO (derived from IMPLEMENTATION_PLAN Scope below — turn each into a concrete deliverable):
${p.scope ? p.scope.split("\n").slice(1).map((l) => "  " + l.trim()).filter((l) => l.length > 2).join("\n") : "  - TODO"}

**Out of scope (later phases)**
- TODO — name what is deferred and to which phase.

**Depends on.** ${bullets(p.dependsOn, "TODO")}

---

## 2. Prerequisites (Windows / PowerShell)

| Tool | Min version | Install (PowerShell) | Verify |
|---|---|---|---|
| TODO | TODO | TODO | TODO |

> Most phases need nothing new beyond ${prev}. Delete this table if so, or list only the additions.

---

## 3. Tech stack & versions

| Concern | Choice |
|---|---|
| TODO | TODO |

---

## 4. Target file tree (delta on ${prev})

\`\`\`text
TODO — only the files this phase ADDS or CHANGES, not the whole repo.
internal/modules/<name>/
├── domain/
├── service/
├── repo/
├── handler/
└── port.go
\`\`\`

---

## 5. Execution steps

Work top to bottom; each step ends in a check.

### S1 — TODO
TODO
**Check:** TODO (a command + expected result).

### S2 — TODO
TODO
**Check:** TODO.

---

## 6. TODO — the <phase> contract / convention

TODO: the one design rule this phase establishes or relies on (e.g. ownership checks, the
table-ownership convention, the outbox contract). Cross-link ARCHITECTURE where relevant.

**Contracts & events (from IMPLEMENTATION_PLAN).** ${bullets(p.contracts, "TODO")}

---

## 7. Configuration reference (additions to ${prev})

| Env var | Type | Default | Required from |
|---|---|---|---|
| TODO | TODO | TODO | ${phaseId} |

> If this phase adds no config, say so and delete the table.

---

## 8. Full file contents

TODO: the complete, paste-ready contents of every new/changed file from §4, each under its path.

---

## 9. Testing plan

| Test | File | Asserts |
|---|---|---|
| TODO | TODO | TODO |

Run: \`task test\` (and \`task test:integration\` for DB-backed tests).

---

## 10. Definition of Done

${seedDoD(p.dod)}
- [ ] \`task ci\` is green (tidy, generate, lint, test, build).

---

## 11. Verification (PowerShell)

\`\`\`powershell
# 1. Build pipeline
task ci

# 2. TODO — exercise this phase's outcome end-to-end (curl/Invoke-RestMethod the new endpoints).
\`\`\`

---

## 12. Handoff to ${next} (TODO: next phase name)

TODO: the seams this phase leaves for ${next} — what plugs in with no rework.
`;

  writeFileSync(out, md);
  console.log(`scaffolded ${out}`);
  console.log(`seeded from IMPLEMENTATION_PLAN entry for ${phaseId}: "${p.title}"`);
  console.log(`next: fill every TODO, then run:  node ${rel(import.meta.url)} check "${rel("file://" + out)}"`);
}

function seedDoD(dodBlock) {
  // The IMPLEMENTATION_PLAN DoD is one prose sentence; turn it into the first checkbox as a seed.
  const text = dodBlock
    ? dodBlock.replace(/^-\s+\*\*[^*]+\*\*:?\s*/, "").replace(/\*Demo:.*$/s, "").trim()
    : "TODO: the demoable outcome";
  return `- [ ] ${text || "TODO: the demoable outcome"}\n- [ ] TODO: add the concrete, checkable acceptance criteria for this phase`;
}

// ---- `check`: validate ----------------------------------------------------

function check(target) {
  let files;
  if (target) {
    files = [target];
  } else {
    if (!existsSync(EXEC_DIR)) die(`no ${EXEC_DIR}`);
    files = readdirSync(EXEC_DIR)
      .filter((f) => /^P\d+.*\.md$/.test(f) && f.toUpperCase() !== "README.md")
      .map((f) => join(EXEC_DIR, f));
  }
  let failed = 0;
  for (const f of files) {
    const problems = checkOne(f);
    if (problems.length) {
      failed++;
      console.log(`FAIL  ${rel("file://" + f)}`);
      for (const p of problems) console.log(`        - ${p}`);
    } else {
      console.log(`ok    ${rel("file://" + f)}`);
    }
  }
  if (failed) {
    console.log(`\n${failed}/${files.length} plan(s) have problems.`);
    process.exit(1);
  }
  console.log(`\nall ${files.length} plan(s) conform.`);
}

function checkOne(file) {
  if (!existsSync(file)) return [`file does not exist`];
  const md = readFileSync(file, "utf8");
  const problems = [];

  // Title
  if (!/^#\s+Execution Plan\s+[—-]\s+P\d+:/m.test(md))
    problems.push(`missing title line "# Execution Plan — P<n>: <name>"`);

  // Metadata table rows
  for (const k of ["Phase", "Status", "Date", "Outcome", "Module path"]) {
    if (!new RegExp(`\\*\\*${k}\\*\\*`).test(md)) problems.push(`metadata table missing **${k}** row`);
  }

  // Companion doc links
  if (!/\(\.\.\/PRD\.md\)/.test(md)) problems.push(`missing companion link to ../PRD.md`);
  if (!/\(\.\.\/ARCHITECTURE\.md\)/.test(md)) problems.push(`missing companion link to ../ARCHITECTURE.md`);

  // The 12 sections, in order
  let lastIdx = -1;
  for (const s of SECTIONS) {
    const m = md.match(s.re);
    if (!m) {
      problems.push(`missing section ${s.n} (${s.title})`);
      continue;
    }
    if (m.index < lastIdx) problems.push(`section ${s.n} (${s.title}) is out of order`);
    lastIdx = m.index;
  }

  // Section-content rules
  if (!/\*\*In scope\*\*/.test(md)) problems.push(`§1 Overview must have an **In scope** list`);
  if (!/\*\*Out of scope/.test(md)) problems.push(`§1 Overview must have an **Out of scope** list`);
  if (!/^###\s+S1\b/m.test(md)) problems.push(`§5 must start its steps at "### S1"`);
  if (!/\*\*Check:\*\*/.test(md)) problems.push(`§5 steps must each end in a "**Check:**" line (none found)`);
  if (!/-\s+\[ \]|-\s+\[x\]/.test(md)) problems.push(`§10 Definition of Done must be a "- [ ]" checklist`);
  if (!/```powershell/.test(md)) problems.push(`§11 Verification must contain a \`\`\`powershell block`);

  // Unfilled scaffold markers
  const todos = (md.match(/TODO/g) || []).length;
  if (todos > 0) problems.push(`${todos} unresolved "TODO" marker(s) — finish the scaffold before shipping`);

  return problems;
}

// ---- utils ----------------------------------------------------------------

function rel(u) {
  const p = (u.startsWith("file://") ? u.slice(7) : u).replace(/\\/g, "/").replace(/^\/([A-Za-z]:)/, "$1");
  const root = REPO.replace(/\\/g, "/");
  return p.startsWith(root) ? p.slice(root.length).replace(/^\//, "") : p;
}
function die(msg) {
  console.error(`error: ${msg}`);
  process.exit(2);
}

// ---- main -----------------------------------------------------------------

const [cmd, a1, a2] = process.argv.slice(2);
switch (cmd) {
  case "new":
    if (!a1) die(`usage: plan.mjs new <PHASE-ID> [slug]   e.g. plan.mjs new P4`);
    scaffold(a1, a2);
    break;
  case "check":
    check(a1);
    break;
  default:
    console.log(`usage:
  node .claude/skills/new-execution-plan/plan.mjs new   <PHASE-ID> [slug]   scaffold a plan from IMPLEMENTATION_PLAN
  node .claude/skills/new-execution-plan/plan.mjs check [path]              validate one plan, or all of docs/executions/`);
    process.exit(cmd ? 2 : 0);
}
