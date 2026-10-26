---
id: P044
title: Unverified discipline and plan triggers
type: feature
status: active
created: 2026-10-26
permissions: auto
gate: go test ./...
executor: pi | strata/swift-1.5-iq3_s | high
fallback: pi | zai/glm-5.3-flash | high; codex | gpt-6-luna | max; claude | claude-sonnet-5-5 | high
review: codex | gpt-6.1-sol | high; claude | claude-opus-5-5 | high
acceptance: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
replan: claude | claude-opus-5-5 | high; codex | gpt-6.1-sol | high
---
## Goal

`Unverified:` items name only a risk of the unit's own change that its
`Verify` does not cover: no copies of dependency items, no test-improvement
ideas, no unfinished `Do:`/Goal work passed off as unverified. Every open
item ends with a disposition — closed by a test, accepted with a reason in
the archive entry, or a Backlog line for a Goal gap or a real risk. An agent
never creates a plan, an archive note, or an activation without an explicit
request.

## Context

- docs/design/cycle.md:L60-76

## Constraints

- Plan format stays `docflow: 3`; the result JSON keeps its keys
  (`status`, `handoff`, `decisions`, `unverified`, `reason`, `blocker`).
- An empty `unverified` list stays legal and means "every risk of this
  change is covered by `Verify`"; the fix narrows what goes in, it does not
  forbid the field.
- Skill sources live only in `skills/`; never edit installed skill
  directories.
- Language: code, comments, skills, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and the
  full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- The runner prompt (`RunnerCycle` in `internal/agents/agents.go`) and the
  `bsv-docflow-run` skill state the same rules; a wording change goes to
  both.

## Decisions

- Narrow the field instead of forbidding it — a forbidden or penalised list
  teaches agents to hide gaps — rejected: fail `done` on any non-empty
  `unverified`.
- Unfinished `Do:`/Goal work is not `unverified`: the agent finishes it or
  blocks with `blocker: "plan"` — the runner cannot tell "did not check"
  from "did not do", only the agent can — rejected: a runner heuristic over
  item text.
- The runner drops an item whose normalised text equals an `Unverified:`
  item of a direct `Depends` unit — agents copy inherited items with a
  "(inherited from …)" tail, and a copy inflates the count three units in a
  row — rejected: fuzzy similarity (false drops of distinct items).
- An accepted risk stays in the archive entry as
  `Accepted: <item> — <why it is not a risk>`, like `Waits:` — closing must
  be able to drop an item without a Backlog line; the agent may accept, the
  reason is recorded — rejected: only a person may drop an item (every
  close would stop for a human).
- A phase run by a `check` provider holds at most 4 units before its review
  unit — one review per phase keeps review cost per unit low while the diff
  stays small enough to review — rejected: a review after every unit.
- Нормализация срезает ровно один хвост `( … )` (unverifiedKey) — чтобы копия «X (inherited from P001.1.1)» совпала с оригиналом, а свой пункт (`layout on iPhone (small)`) остался различим — отвергнуто: срезать все скобки (теряются реальные риски) и нечёткое сходство (ложные потери пунктов).
- Текст про `Accepted:` не добавлен в docs/design/cycle.md в этом юните — docs фиксируют текущее поведение, а не решение плана — отвергнуто: записать решение заранее (факт появится только в P044.2.1, а дубль потом придётся вычищать).
- skills/bsv-docflow-run/SKILL.md синхронизирован, хотя `Do:` его не называет — ловушка плана требует держать текст раннер-цикла и скилл одинаковыми — отвергнуто: оставить скилл в старом смысле (агент увидит два разных правила).

## Phase 1: Rules

### [x] P044.1.1 Narrow unverified in the runner
Read:
- internal/agents/agents.go:L137-180
- internal/cycle/commands.go:L40-75
- internal/runner/run.go:L990-1060
- internal/runner/progress.go:L405-420
- docs/design/runner.md:L250-262
Do:
- In `RunnerCycle` (`internal/agents/agents.go`) replace the `unverified`
  bullet: "unverified: a risk of what this unit changed that `Verify` does
  not cover, each naming the missing check (layout on a device, a flow
  `Verify` does not run, a case without a test). Not: items already listed
  under `## Handoff from Depends`, ideas to improve tests, checks of code
  this unit did not change. A `Do:` step or Goal requirement left undone is
  not unverified: finish it, or reply blocked with blocker \"plan\". An
  empty list claims every risk of this change is covered by `Verify`."
- In the prompt builder (`internal/cycle/commands.go`), right after the
  `## Handoff from Depends` heading, add the line: "`Unverified:` items
  below stay open on their own units; do not repeat them in your
  `unverified`."
- In `internal/runner/run.go`, where `result["unverified"]` becomes Handoff
  items: drop an item whose normalised text equals the normalised text of
  an `Unverified:` item in the Handoff of a direct `Depends` unit.
  Normalisation: lower case, whitespace collapsed, one trailing
  parenthetical `( … )` and trailing punctuation removed. Count, print and
  write only kept items; with `--verbose` print
  `unverified: N repeated from Depends dropped`.
- Tests: the prompt text (the new bullet and the Depends line); a fake-agent
  run whose dependency Handoff holds `Unverified: X` and whose reply lists
  `X (inherited from P001.1.1)` and `Y` → the plan Handoff gets only
  `Unverified: Y`, the summary says `1 unverified`.
- Docs: `docs/design/cycle.md` bullet about `Unverified: …` and
  `docs/design/runner.md` paragraph about `unverified` — the narrowed
  meaning and the dedup, in Russian. `CHANGELOG.md` `## [Unreleased]` →
  `### Changed`.
Verify: `go test ./internal/agents/ ./internal/cycle/ ./internal/runner/`
Traps: The Depends Handoff is plan text: read it through the parsed plan
(`plan.Unit(id).Field("Handoff")`), the same way `commands.go` builds the
prompt; do not re-parse the prompt string.
Depends: -
Commit: P044.1.1: narrow unverified items in runner prompt
Handoff:
- RunnerCycle закреплён SHA-256 в TestStrictRunnerSchema (internal/agents/agents_test.go): любое изменение текста раннер-цикла требует пересчёта хеша, сейчас d80ff9b59c2e320a16a23e556956791c0f7a155bac90d1fd6a148846fffa3784.
- Правило дедупа живёт в unverifiedKey (internal/runner/run.go): одна нормализация на обе стороны. P044.2.1 (Accepted, диспозиции закрытия) сверяет пункты тем же ключом — дублировать нормализацию нельзя.
- Формулировка unverified дублируется в skills/bsv-docflow-run/SKILL.md (Traps плана). Я синхронизировал её; установленные копии скиллов не обновлял — нужен bash tools/install-dev.sh.
- docs/design/cycle.md ссылается на ../archive/P044-unverified-discipline.md, файла ещё нет — его создаст закрытие P044; lint ссылки в docs не проверяет, так что битую ссылку lint не покажет.
- Пункт `Accepted: <item> — <почему это не риск>` в docs не добавлял: docs описывают текущее поведение, а закрытие это сделает в P044.2.1 — тогда и ссылка на архивную запись.
- Строка Depends проверяется тестом на фикстуре P001.1.3 (internal/cycle/cycle_test.go): позиция между заголовком `## Handoff from Depends` и первым `### Pxxx`. Юнит без Depends секцию не печатает — строки там не появляется.
- Unverified: Прогоны `bash tools/test-install.sh` и `bash tools/parity/run.sh bin/docflow bin/docflow` (CI-шаги, не входят в Verify/gate) не запускались — missing check: прогнать их на этом дереве, вывод раннера с новой verbose-строкой там не проверен.
- Unverified: Эффективность новой формулировки на живом агенте (перестанет ли он наследовать чужие `Unverified:` до того, как сработает дедуп) — missing check: реальный `docflow run` по плану с Depends и просмотр Handoff юнитов после прогона.

### [ ] P044.1.2 Tighten skill rules for unverified and plans
Read:
- skills/bsv-docflow-run/SKILL.md:L88-157
- skills/bsv-docflow-plan/SKILL.md:L16-45
- skills/bsv-docflow-plan/SKILL.md:L122-183
- skills/bsv-docflow-plan/SKILL.md:L219-267
- skills/bsv-docflow-plan/SKILL.md:L393-403
- skills/bsv-docflow-close/SKILL.md:L18-77
- internal/cycle/lifecycle.go:L15-30
- docs/design/format.md:L96-106
Do:
- `bsv-docflow-run`, section Handoff: replace the `Unverified:` bullet with
  the rule `RunnerCycle` now states (read it in
  `internal/agents/agents.go`). Section "When to block", the review
  paragraph: observations without a defect are plain Handoff items, not
  `Unverified:`.
- `bsv-docflow-plan`, section When: before any plan file, `docflow note`,
  `activate` or `close`, ask the user one question — "make a plan or do it
  now?" — whatever the task's size; never decide it for the user.
  Procedure step 2: after work without a plan, no `docflow note` unless the
  user asks; when the work changed a design decision, offer the note in the
  reply. Section "What not to do": add "Do not write `docflow note` without
  a request".
- `bsv-docflow-plan`, section Unit, the `Verify:` bullet — add the rule
  "Verify covers Do": when `Do:` changes behavior the `Verify` command
  cannot observe (a screen, an animation, a device flow), add a UI or e2e
  test to `Verify:`, or schedule a live-check unit right after the first
  such unit of the phase, not only in the final acceptance.
- `bsv-docflow-plan`, section Review: observations without a defect are
  plain Handoff items; a phase run by a `check` provider holds at most 4
  units before its review unit — a longer stretch splits into phases.
  Section Acceptance: every `Unverified:` item of earlier Handoffs ends
  closed by a test, or as `Accepted: <item> — <why it is not a risk>`, or
  stays `Unverified:` only when it is a Goal gap or a risk with a concrete
  failing scenario.
- `bsv-docflow-close`, steps 4 and 8: replace "Always moved, no judgment"
  with a triage of each open `Unverified:` item — verify it now; or write
  `Accepted: <item> — <why>` in Follow-ups, which stays only in the entry,
  like `Waits:`; or move it to the Backlog when it is a Goal gap or a risk
  with a concrete failing scenario. Unfinished Goal work always goes to the
  Backlog.
- `docs/design/format.md`, the Backlog paragraph: `Accepted:` items stay in
  the entry like `Waits:` (Russian).
- `internal/cycle/lifecycle.go`: `agentsLine` becomes
  `Plans: docflow (run docflow status), only on an explicit request; not the bsv-docs PLAN genre`;
  update the tests that pin it.
Verify: `go test ./internal/cycle/ && python3 tools/test-check-plan-skill.py && grep -q 'Verify covers Do' skills/bsv-docflow-plan/SKILL.md && grep -q 'Accepted:' skills/bsv-docflow-close/SKILL.md && ! grep -q 'no judgment' skills/bsv-docflow-close/SKILL.md && go build -o bin/docflow ./cmd/docflow && bin/docflow lint`
Traps: `bin/docflow lint` checks this plan file too; do not edit the plan.
Depends: P044.1.1
Commit: P044.1.2: tighten unverified and plan trigger rules

### [ ] P044.1.3 Review the rules phase
Read:
- docs/design/cycle.md:L60-76
- docs/design/format.md:L96-106
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Check that the
  runner prompt and the `bsv-docflow-run` skill state the same
  `unverified` rule. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P044.1.3: review unverified rules

## Phase 2: Acceptance

### [ ] P044.2.1 Accept the unverified discipline
Read:
- tools/parity/README.md:L1-60
- tools/parity/scenarios/run_cli.sh
Do:
- One scenario per Goal statement through the built CLI with the fake
  agent, as a new parity scenario `tools/parity/scenarios/run_unverified.sh`:
  1. Two-unit plan, unit 2 depends on unit 1; unit 1 replies
     `unverified: ["X"]`, unit 2 replies
     `unverified: ["X (inherited from P001.1.1)", "Y"]` → unit 2's Handoff
     holds only `Unverified: Y`; the summary line says `2 unverified`.
  2. `docflow prompt` of unit 2 contains the "stay open on their own units"
     line under `## Handoff from Depends` and the narrowed `unverified`
     bullet.
  3. `docflow init` in a fresh repository writes the new AGENTS.md line.
- Close every `Unverified:` item of earlier units with a test, or give it a
  disposition as the plan skill's Acceptance section now requires.
Verify: `go build -o bin/docflow ./cmd/docflow && bash tools/parity/run.sh bin/docflow bin/docflow && go test ./...`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P044.1.1, P044.1.2, P044.1.3
Role: acceptance
Commit: P044.2.1: accept unverified discipline
