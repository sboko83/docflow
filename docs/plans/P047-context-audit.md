---
id: P047
title: Context growth audit
type: perf
status: active
created: 2026-10-26
permissions: auto
gate: go test ./...
executor: pi | zai/glm-5.3-flash | high
fallback: pi | zai/glm-5.3 | high; pi | strata/swift-1.5-iq3_s | medium; claude | claude-sonnet-5-5 | high
heavy: pi | zai/glm-5.3 | high; claude | claude-opus-5-5 | high
review: claude | claude-sonnet-5-5 | high
acceptance: claude | claude-opus-5-5 | high
replan: claude | claude-opus-5-5 | high
---
## Goal

The context of a docflow session does not grow with the number of closed
plans: a measurement tool reports every input docflow assembles or tells an
agent to read, with its size now and projected at 200 closed plans, and
fails on a threshold; a report shows what agents actually read beyond a
unit's `Read:`; `lint` warns when a `Read:` item pulls a whole large file,
whose archive Gotchas grow with every plan linked from it.

## Context

- docs/design/cycle.md:L77-126

## Constraints

- Tools in `tools/` are `bash` 3.2 or `python3` stdlib only; Go code uses
  the standard library.
- Plan format stays `docflow: 3`; a new optional `docs/docflow.md` key
  needs no version bump, but the format spec, `templates/` and `lint`
  change together.
- `docs/design/runner.md` keeps its content; restructuring it into
  sections must not change any decision it states.
- Language: code, comments, `README.md`, `CHANGELOG.md` in English;
  `docs/` in Russian.
- A unit does not commit and does not rewrite history; the runner commits
  with the unit's `Commit:` line.

## Traps

- `go test ./...` takes ~3 min; run a package's tests while working and the
  full gate once at the end.
- `docflow` may be missing from `PATH`; build the checkout binary with
  `go build -o bin/docflow ./cmd/docflow` (`bin/` is gitignored).
- `docflow prompt` appends `## Archive gotchas`: the Gotchas of every
  archive entry linked from the plan's Context/Decisions and from the text
  each `Read:` item references (`ArchiveGotchas` in
  `internal/format/ref.go`). A whole-file reference pulls the Gotchas of
  every entry the file links; a range or `#anchor` pulls only those linked
  inside it. Measured at planning time: whole `docs/design/runner.md` is
  ~12k tokens plus ~4.5k tokens of Gotchas from 20 entries.
- `docs/design/runner.md` has a single H2 heading, so `#anchor`
  references into it are useless until it gets sections.

## Decisions

- Bound the per-unit cost by narrowing references, not by capping
  Gotchas — a trap from a linked entry is relevant to that area, and the
  range a unit reads decides which entries are linked — rejected: a fixed
  byte cap on `## Archive gotchas` (drops traps at random).
- A whole-file `Read:`/Context item over `whole_read_limit` tokens (new
  optional `docs/docflow.md` key, default 4000) is a `lint` warning, not an
  error — existing archived references and small projects stay valid —
  rejected: an error (breaks running plans on upgrade).
- Средний рост на план считается по окну после squash (базовая точка — родитель коммита закрытия P035, 12 планов) — вся история до публичного релиза сжата в один коммит, P001–P034landed together — rejected: средняя по всей истории (делить на 46 закрытых плана занижает рост вчетверо).
- Рост prompt-строк моделируется из их источников: сумма прироста `## Gotchas` каждой связанной записи плюс средний размер Gotchas на число новых связей в план, умноженное на долю ссылок, сохраняемую диапазонами — самого fixture-плана нет внутри git-окна, измерить его рост по истории нельзя — rejected: эмпирический рост prompt по историческим юнитам (таких fixture-планов в истории нет) и фиксированный нулевой рост (дешёвый prompt навсегда дешёвый, ловушки не видно).
- В fixture целый ref и диапазон указывают на один и тот же docs/design/runner.md, вариант «только диапазоны» берёт runner.md:L10-12 и docs/design/cycle.md:L94-126 — `--check` бюджетит именно его — rejected: новый topic-файл для fixture (он не растёт с закрытыми планами и ничего не измеряет).
- Юниты fixture остаются `[~]` в копии с одним `git init` — prompt для `[~]` не требует чистого дерева и не проверяет Depends — rejected: полная копия с коммитом (нужна конфигурация git-пользователя, медленнее, и prompt пачкает дерево сразу после первого вызова).
- Мерить чтение на коммите юнита, при отсутствующем коммите — на коммите закрытия плана, для юнита в работе — по рабочему дереву — размеры должны соответствовать эпохе (P011–P016 читали Python bin/docflow) — отвергнут вариант «всё по рабочему дереву» (одно Read bin/docflow дало 47M токенов: 190MB Go-бинарь代替 400KB скрипта).
- Ограничивать каждое чтение выходным лимитом инструмента (50KB / 2000 строк) и делить этот лимит на файлы одной команды — больше инструмента не доходит — отвергнут размер файла целиком (`head -1` 200MB лога стоил 50M токенов, и top был забит логами раннера).
- Искать коммит юнита по `git log --all` — коммиты до публичного релиза после сквашивания недостижимы из HEAD — отвергнут поиск по HEAD (у восьми ранних планов теряется файл плана, read_list=0, все чтения становятся лишними).
- Юнит без файла плана в истории помечать отдельно (`unmeasured units`) и не включать в агрегат — отвергнуто считать его чтения лишними (это артефакт сквашивания, а не привычка агента).
- Лечить падение --check урезанием README.md, а не блоком — ловушка юнита прямо разрешает size cut названного файла, а ~10% сжатия установочных абзацев переводит строку в ok; отвергнуто: blocked с blocker plan (цикл replan ради конденсации двух абзацев)
- Context предупреждать только у планов с открытым юнитом — Context кормит промпты лишь открытых юнитов; отвергнуто: предупреждать Context всех планов (шум по закрытой истории)
- M в сообщении — байты/4 отформатированных строк '- (id) …' ровно тех Gotchas, что тянет ArchiveGotchas (суперседенные записи пропускаются) — цифра совпадает с реальным размером ## Archive gotchas в промпте; отвергнуто: число связанных записей без размера (не показывает стоимость)
- После дефектов ревью P047.1.4 заменить его юнитами P047.1.5 (исправления и регрессионные проверки) и P047.1.6 (повторное ревью) — проверка бюджета описаний обязана использовать проекцию, а исторические чтения не должны зависеть от наличия пути сегодня; отвергнуто: исправлять код в review-юните или принимать отчёт с потерянными чтениями.
- Разрешение путей идёт по списку файлов измеряемого коммита (tree_paths, один ls-tree -r --name-only на юнит, кэш), и только потом по диску — файл эпохи юнита, удалённый позже, остаётся чтением; отвергнуто: мерить только рабочим деревом (теряет 601 токен за чтение) и забирать все блобы коммита (вся tree на каждый из 91 юнита).
- Shell-глобы раскрываются и по дереву коммита, и по диску, сопоставление посимвольно по компонентам пути (fnmatchcase) — shell `*` не пересекает `/`, у плоского fnmatch пересекает; отвергнуто: глоб только по диску (удалённый файл невидим) и плоский fnmatch (docs/* именует docs/a/b.md).
- Бюджет описаний скиллов проверяется по проекции на --plans, а текущий размер сравнивается с лимитом только когда он уже выше — надобно поймать перерасход до пробоя, а сжимающаяся проекция не должна оправдывать существующий пробой; отвергнуто: сравнивать только текущий размер (дефект ревью) и печатать обе проверки всегда (дубль строки для здорового репозитория).
- Регрессии живут в новом tools/test-context-audits.py и не прописаны в gate — их зовёт Verify приёмки; отвергнуто: тащить python-сценарии в go test ./... и в parity-сценарии (инструменты не являются CLI).
- Каждая строка бюджета в `context-audit.py --check` падает и по проекции, и по текущему размеру, если он уже выше лимита — убывающая за окно проекция не должна скрывать существующий пробой ни в одной строке — отвергнуто: зафиксировать, что отрицательное окно роста не поддерживается (пробой при `--window-plan P046` остался бы тихим).
- read-audit берёт в аудит все юниты из `docflow stats` и pi-логов; юнит без читаемых вызовов инструментов попадает в `skipped` — так отчёт честно показывает, какая доля юнитов не измерена — отвергнуто: оставить фильтр source=transcript (61 юнит на Codex и `claude -p` исчезал из отчёта молча).

## Phase 1: Measure and bound

### [x] P047.1.1 Add the context audit tool
Read:
- internal/format/ref.go:L180-340
- internal/cycle/commands.go:L20-90
- internal/lint/lint.go:L270-330
- docs/design/format.md:L55-80
Do:
- Write `tools/context-audit.py` (python3 stdlib), run from the repository
  root, measuring proxy tokens (bytes / 4) of:
  1. Start reads: `docflow status` output plus every `start_read` file.
  2. Skills: the `description:` of every `skills/*/SKILL.md` (always in
     an agent's context when installed) and each skill body.
  3. Every `docs/design/*.md` topic and `docs/usage.md`: size, distinct
     archive entries it links, bytes of their `## Gotchas`.
  4. `docs/docflow.md`: total and the `## Backlog` section.
  5. A `docflow prompt` of a regular and of an acceptance unit of a
     fixture plan in a temporary copy of the repository (both `Read:` a
     whole `docs/design/runner.md` and one line range of it).
- Projection to 200 closed plans: from `git log` of `docs/design/` and
  `docs/archive/`, the average growth per archived plan of each topic's
  size and of its linked Gotchas; print now / per plan / at 200 per row.
- `--check`: exit 1 when, at 200 plans, start reads exceed `start_budget`,
  all skill descriptions together exceed 1500 tokens, or the regular-unit
  prompt with only ranged reads exceeds half of `read_budget`; print the
  failing rows.
- `--json` prints the same rows as JSON.
Verify: `go build -o bin/docflow ./cmd/docflow && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --json | python3 -c 'import json,sys; rows=json.load(sys.stdin); assert any(r["name"].endswith("runner.md") for r in rows)'`
Traps: `git log` history before the public release is squashed: every
archive entry up to P034 lands in one commit; compute growth per plan
from P035 on.
Depends: -
Commit: P047.1.1: add context audit tool
Handoff:
- --check падает на этой ветке: строка start-reads = 6887 токенов при 200 планах против start_budget 4000 (README.md +28 токенов/план, docs/design/README.md +0, вывод `docflow status` ~33 токена и не растёт). Трап P047.1.3 это допускает: правка — сокращение README.md или сужение start_read, повышение бюджета не является правкой размера.
- Механика fixture-копии: `docflow prompt` требует git-репозиторий (git rev-parse/status) и для юнита `[ ]` отказывает на грязном дереве, при чужом `[~]` и на незакрытых Depends. Поэтому fixture копирует `git ls-files` во временный каталог, делает `git init` без коммита, а все три юнита записаны как `[~]`. Юниты плана — H3 под H2-фазой: заголовок `#### [ ] P999.1.1` даёт «no unit P999.1.1».
- Цифры для P047.1.3: целый `Read:` на docs/design/runner.md тянет 22 связанные архивные записи — prompt 5484 токена против 1280 у варианта с диапазонами (at200: 57138 против 3924); сам runner.md 13907 токенов, его связанные Gotchas 4949 токена и +202 на план. Целый ref дороже диапазона на ~4200 токенов сегодня и на ~53000 при 200 планах.
- Окно роста задаётся флагом `--window-plan` (default P035): базовая точка — родитель коммита закрытия этого плана, число планов = записи архива, добавленные от него до HEAD. Новый squash (например после P060) лечится флагом, правки кода не требует.
- Колонки ent/gotch/g-at200 заполнены только у kind=design; у prompt-строк их архивная нагрузка внутри токенов, список вытянутых записей — в поле detail. P047.1.2 опирается на имена строк: prompt:regular:whole+range, prompt:regular:ranged, prompt:acceptance:whole+range.
- tools/__pycache__ появляется, если tools/*.py импортируют (py_compile, importlib); в .gitignore строки нет, а `finish` берёт `git add -A` (P009) — удалять каталог перед finish. В этом юните он удалён, в коммит не попадёт.
- Unverified: Ветка отказа `--check` (exit 1 и печать провалившихся строк) прогнана только вручную в этой сессии: Verify юнита запускает `--json` без `--check`, в CI (gate, test-install, parity) инструмент не вызывается — отсутствует сценарий, который гоняет `python3 tools/context-audit.py --check` и ждёт ненулевого кода на перерасходе бюджета (это берёт на себя Goal-пункт 1 в P047.2.1).
- Unverified: Точность проекции: нет контрольной точки, на которой предсказанный рост строки совпадает с фактическим, — отсутствует проверка «закрыть следующий план и сравнить фактические размеры runner.md и его связанных Gotchas с предсказанием at(N+1)».
- Unverified: Переносимость git-обвязки (`ls-tree -r`, `cat-file --batch`, `ls-files -z`, `git init` в копии) проверена только на macOS arm64 и python3 из Xcode — отсутствует прогон `python3 tools/context-audit.py --json` в矩阵 CI (ubuntu-latest, windows-latest/Git Bash), где parity-сценарии уже запускаются.

### [x] P047.1.2 Report reads beyond a unit's Read list
Read:
- internal/stats/stats.go:L1-80
- docs/design/cycle.md:L94-126
Do:
- Write `tools/read-audit.py` (python3 stdlib). For each unit that
  `docflow stats` lists with source `transcript` or a pi `.out` log in
  `<git-dir>/docflow/run/`: take the unit's `Read:` list from the plan file
  as of the unit's commit (`git log` of `docs/plans/<plan>-*.md`), collect
  the files the agent read (Claude `Read` tool calls and `cat`/`sed`/
  `head`/`grep` file arguments in Bash calls; pi `read` and `bash` tool
  calls), and drop the files the unit's commit changed.
- Print per unit: files and approximate tokens read beyond `Read:`; then
  the top 15 paths by total extra tokens across units, and the share of
  extra reads in all reads.
- Explain the jump of `base_ctx` in `docflow stats` from ~33k (P035) to
  ~44k (P036, P041): compare, at the commits of those units, the sizes of
  `AGENTS.md`, `CLAUDE.md`, the skill descriptions and the start reads;
  print which of them changed by more than 2k tokens, or
  `base_ctx: not explained by docflow inputs`.
- Record the numbers in the Handoff (top paths, share, the base_ctx
  verdict); no doc file holds measurement output.
Verify: `python3 tools/read-audit.py --limit 5 | grep -q '^top '`
Traps: Claude transcripts live in `~/.claude/projects/<cwd with / → ->/`;
the transcript of a unit is found the way `internal/stats` finds it. Do
not print file contents, only paths and sizes.
Depends: P047.1.1
Commit: P047.1.2: report reads beyond unit read lists
Handoff:
- Цифры read-audit (полный прогон, 89 юнитов, skipped 0, unmeasured 0): лишние чтения 4231/5668 вызовов = 74.6%, лишние токены 2054632/4404553 = 46.6%, в среднем 23k лишних токенов на юнит. Top-8 по лишним токенам (скобки = число юнитов): bin/docflow 196757 (6), internal/runner/run_test.go 95071 (15), internal/lint/lint.go 89481 (4), cmd/docflow/lifecycle_test.go 63376 (11), internal/agents/agents.go 61339 (12), cmd/docflow/main.go 54609 (12), docs/usage.md 53070 (5), docs/design/format.md 48367 (15). Худшие юниты: P036.2.2 136k, P045.1.2 109k, P035.1.2 105k, P045.1.1 102k (1694 из 1730 чтений).
- base_ctx: P035 median 34659 (unit P035.3.1 d18c9ee) -> P036 42561 (+7902, P036.4.1 05a2f21) -> P041 44825 (+10166, P041.2.1 b0ba86b). Входы docflow на этих коммитах: AGENTS.md 789->915, CLAUDE.md 2->2, описания скиллов 628->628, start reads 2202->2359; суммарный сдвиг +41 и +283 токенов, ни один вход не изменился на 2k, поэтому печатается `base_ctx: not explained by docflow inputs`. Вывод: рост base_ctx даёт среда агента (системный промпт, схемы инструментов, глобальный ~/.claude/CLAUDE.md — вне git), Goal-ограничение «контекст не растёт с числом планов» надо вешать на входы docflow и на лишние чтения юнита, а не на base_ctx.
- Лишние чтения — это соседние файлы той же темы: тесты пакета, который правят, генерируемый CLI (bin/docflow не в git -> мерится по рабочему дереву), design-топики (format.md, usage.md) и установленные копии скиллов (~/.pi/agent/skills/*/SKILL.md).Lint с `whole_read_limit` (P047.1.3) сузил только списки `Read:` и эти чтения не срежет: их в `Read:` нет вовсе.
- Соглашения measure, с которыми обязан совпасть P047.1.3/P047.1.4: область действия — файл целиком (диапазон в `Read:` делает в области весь файл, чтения вне диапазона не считаются лишними); размер одного чтения ограничен выходным лимитом инструмента 50KB/2000 строк, команда с несколькими файлами делит этот лимит между файлами; размер берётся из блоба коммита юнита, при его отсутствии — с коммита закрытия плана (строка `unit` это помечает), для юнита в работе — из рабочего дерева; прокси токенов = bytes/4, как в lint и tools/context-audit.py.
- unit_commit ищет по `git log --all`: корень публичной истории c62f326 не имеет родителя, а коммиты юнитов P011–P016 из HEAD недостижимы. Инструмент, который смотрит только HEAD, теряет файлы планов этих восьми планов, read_list становится 0 и все чтения становятся лишними. Следующим юнитам, которые трогают ранние планы, нужен --all.
- Телеметрия раннера (.git/docflow/run/*.out и *.events.jsonl) считается лишним чтением: юниты планов-отладок раннера её выгружают (P045.1.1 читал один .out 45 раз через `head -c 10000` = 450KB). Если приёмка P047.2.1 сочтёт это шумом — фильтровать в инструменте, не переписывать цифру в документ.
- Флаги: --limit N = N последних юнитов в порядке планов (3 с), полный прогон по 89 юнитам = 17 с, --jump по умолчанию P035,P036,P041, --json даёт keys units/top(path,tokens,units)/share/skipped_units/base_ctx.
- tools/__pycache__ появляется, если read-audit.py импортируют (отладочный harness через importlib его импортировал); каталог удалён, строки в .gitignore нет — удалить перед finish, иначе `git add -A` утащит его в коммит юнита.
- Unverified: Полный прогон по всем 89 юнитам не входит в Verify (та гоняет `--limit 5`): падение на данных старого юнита (битая строка транскрипта, кривый блок `Read:`) пройдёт незамеченным — отсутствует сценарий, который гоняет `python3 tools/read-audit.py > /dev/null` в CI.
- Unverified: Ветка `base_ctx changed ...` (вход сдвинулся больше чем на 2k токена) на этом репозитории не срабатывает ни для одной пары планов (+41 и +283) — отсутствует фикстура с планом, у которого AGENTS.md вырос больше чем на 2k между двумя планами, и проверка, что строка `base_ctx changed` печатается.
- Unverified: Юниты на Codex не аудитятся: инструмент читает Claude-транскрипты и pi `.out` логи, `skipped units 0`. Отсутствует сценарий с юнитом `codex exec --json`, который требует попасть в `skipped` — сейчас нулевой счётчик не доказывает, что ветка Codex отсутствует, а будущий Codex-юнит выпадет молча.
- Unverified: Разбор shell-команд (`head -n N`, `head -c N`, `sed -n 'A,Bp'`, глобы, префиксы `VAR=`/sudo) проверен только на командах из реальных логов; токены с `$(...)` и process substitution пропускаются по правилу — отсутствует табличный тест на bash_reads/head_bound/sed_bound по образцу tools/test-check-plan-skill.py.
- Unverified: Переносимость: прогон сделан только на macOS arm64 и python3 из Xcode — отсутствует запуск `python3 tools/read-audit.py --limit 5` в矩阵 CI (ubuntu-latest, windows-latest/Git Bash), где проверяются имя каталога транскриптов (`[^A-Za-z0-9]` → `-`), раскрытие `~` и git-обвязка (`cat-file --batch`, `ls-tree -r --all`).
- Unverified: Величина лимита (50KB / 2000 строк) взята из текущей обрезки вывода pi и Claude Code; нет проверки, привязанной к реальному инструменту, — смена обрезки молча меняет все цифры отчёта.

### [x] P047.1.3 Warn on whole reads of large files
Read:
- internal/lint/lint.go:L270-330
- internal/format/ref.go:L180-260
- docs/design/format.md:L55-80
- templates/docflow.md
- docs/design/runner.md:L1-40
Do:
- New optional `docs/docflow.md` key `whole_read_limit` (positive integer,
  tokens, default 4000). `lint` warns for every `Read:` item and every
  `## Context` item of a `[ ]`/`[~]`/`[!]` unit's plan that references a
  whole file over the limit:
  `<plan>:<line>: warning: <path> is ~N tokens whole (+M tokens of archive gotchas); reference a range or #anchor`.
  M is what `ArchiveGotchas` would pull for that file.
- Split `docs/design/runner.md` into H2 sections by its existing topics
  (cycle, result JSON, fallback and timeout, output, events and stats, …)
  without changing any statement; keep every existing anchor that other
  files reference working, or update the references.
- Update this repository's `docs/docflow.md` only if its own plans need a
  different limit; document the key in `docs/design/format.md` and the
  templates comment; `CHANGELOG.md` `### Added`.
- Rewrite whole-file references to large topics in the `bsv-docflow-plan`
  skill's sample and rules, if any, to ranges or anchors.
- Tests in `internal/lint`: warning above the limit, none for a range or
  an anchor, the key's validation, the gotchas count in the message.
Verify: `go test ./internal/lint/ ./internal/format/ && go build -o bin/docflow ./cmd/docflow && bin/docflow lint && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --check`
Traps: `--check` may fail on a row unrelated to whole reads (skill
descriptions, start reads); fix it in this unit only when the fix is a
size cut of the file named in the row; otherwise reply `blocked` with
`blocker: "plan"` and the failing rows.
Depends: P047.1.2
Role: heavy
Commit: P047.1.3: warn on whole reads of large files
Handoff:
- runner.md разбит на H2-секции для ranged-ссылок: #цикл-на-юнит, #смены-исполнителя-и-ожидание-лимита, #авто-replan, #незавершённый-коммит-и-граница-плана, #поток-событий, #старт-прогона, #result-json, #конфиг-и-маршруты, #резервные-маршруты-и-таймаут, #вывод-прогона, #профили-агентов; якорь #резервные-маршруты-и-таймаут из format.md сохранён
- start-reads проходит с запасом 44 токена проекции (3956/4000): каждые +4 байта README.md сейчас съедают ~55 токенов проекции — закрывающим юнитам P047 не раздувать README; устойчивое решение (якорь в start_read или политика бюджета) — уровень плана, строка снова пробьёт лимит при росте README ~12 ток/план
- README.md/README.ru.md сжаты по ловушке юнита (строка start-reads падала ещё на HEAD): установочные детали переехали указателями в docs/usage.md; формат ссылки — [установка и обновление](docs/usage.md)
- lint warns только в lint/activate/thaw (lintRefs), docflow prompt предупреждение не печатает — это поле lint, как в Do
- Unverified: README.ru.md зеркалит урезание по смыслу, но автоматической сверки содержимого двух README нет — отсутствует проверка парности (диф-ревью или тест), русский текст мог потерять деталь, сохранённую в английском
- Unverified: точный Windows-путь запуска (%USERPROFILE%\.docflow\current\docflow.exe) убран из README при сжатии, а docs/usage.md полную Windows-раскладку не описывает — отсутствует проверка полноты установочных доков; фактическая раскладка теперь видна только из install.sh

### [-] P047.1.4 Review the context audit
Read:
- docs/design/format.md:L55-80
Do:
- Review the diff named in the prompt's `## Phase diff` against `## Goal`,
  `## Constraints`, `## Decisions` and each unit's `Do:`. Check that the
  restructured `docs/design/runner.md` states the same decisions as
  before. Edit nothing.
- A defect — reply `blocked` with `blocker: "plan"`; observations without
  a defect — plain Handoff items.
Verify: `go test ./...`
Role: review
Commit: P047.1.4: review context audit
Handoff:
- Blocked (plan): Обнаружены два дефекта. tools/context-audit.py:662 проверяет текущий размер описаний скиллов вместо проекции: воспроизведён exit 0 при 829 токенах сейчас и 3402 при 200 планах, лимит 1500. tools/read-audit.py:483 отбрасывает исторические пути, отсутствующие в рабочем дереве: чтение tests/helpers.py на коммите P011.1.1 теряет 601 токен, хотя файл отсутствует в Read и изменениях юнита. Нужны исправления и повторное ревью.

### [x] P047.1.5 Fix audit projection and historical reads
Read:
- tools/context-audit.py:L499-715
- tools/read-audit.py:L148-183
- tools/read-audit.py:L215-329
- tools/read-audit.py:L475-579
- tools/read-audit.py:L642-718
- tools/test-check-plan-skill.py:L1-80
Do:
- В `tools/context-audit.py` сравнивать суммарные описания скиллов с
  лимитом 1500 по проекции на `--plans` (по умолчанию 200), а не по
  текущему размеру; согласовать текст отказа и поле `check` в JSON.
- В `tools/read-audit.py` разрешать пути относительно выбранного
  исторического snapshot, включая файлы, которых больше нет в рабочем
  дереве. Сохранить правила выбора коммита, исключения `Read:` и
  изменённых файлов, лимиты вывода и чтения внешних/незакоммиченных файлов.
  Проверить оба входа: прямой `Read`/`read` и shell-команды.
- Добавить `tools/test-context-audits.py` на python3 stdlib с временными
  git-репозиториями и синтетическими логами, без живого агента и личных
  транскриптов. Сценарий бюджета: сейчас ниже 1500, при 200 планах выше
  1500, остальные бюджеты соблюдены — CLI `--check` возвращает 1,
  сообщает строку описаний, `--json --check` помечает её `fail`;
  контрольный сценарий с проекцией в бюджете возвращает 0 и `ok`.
- В том же наборе воспроизвести чтение файла, существующего лишь на
  коммите юнита и отсутствующего в его `Read:` и изменениях: файл
  попадает в лишние чтения с размером исторического блоба через оба
  входа. Контроли: путь из `Read:` и изменённый файл не лишние,
  несуществующий ни в snapshot, ни на диске путь не учитывается.
- Повторить исходный случай P011.1.1 с `tests/helpers.py`: подтвердить
  восстановление 601 токена из Handoff P047.1.4; записать результат в
  Handoff. Если исходный транскрипт недоступен, явно указать это как
  `Unverified`, отдельно от результата синтетической регрессии.
Verify: `go build -o bin/docflow ./cmd/docflow && PYTHONDONTWRITEBYTECODE=1 PATH="$PWD/bin:$PATH" python3 tools/test-context-audits.py && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --check && python3 tools/read-audit.py --limit 5`
Traps: Не повышать бюджеты и не обнулять рост ради зелёной проверки.
Исторический путь нельзя отбрасывать через `os.path.isfile` текущего
дерева до обращения к snapshot; shell-глобы также не должны терять
исторические файлы. Не менять закрытые Handoff с прежними цифрами.
Если исправленная проекция выявит другой перерасход, требующий нового
решения, вернуть `blocked` с `blocker: "plan"` и провалившейся строкой.
Не оставлять `tools/__pycache__` после импортов тестового harness.
Depends: P047.1.1, P047.1.2, P047.1.3
Commit: P047.1.5: fix audit projection and historical reads
Handoff:
- Цифры read-audit после фикса не совпадают с закрытым Handoff P047.1.2 (74.6% / 46.6%): полный прогон даёт 91 юнит, skipped 1, unmeasured 0, лишние чтения 4514/6243 (72.3%), лишние токены 2156200/5195374 (41.5%), 16 с. На паре замеров одного дерева фикс добавляет +422 чтения, +182 лишних и +66879 лишних токенов в 54 из 91 юнитов; худшие: P025.2.2 +13040, P011.1.2 +9937, P025.2.3 +9665, P026.1.2 +8815, P015.3.1 +8299. Closed Handoff с прежними цифрами не править.
- После фикса у двух юнитов лишние токены стали МЕНЬШЕ: P046.2.1 −3150 и P026.2.1 −707. Причина не регрессия: ранее отброшенный путь теперь входит в группу одной bash-команды и делит выходной лимит 50KB, а пункт `Read:`, который разрешается только из истории, переводит чтения из лишних в область.
- Случай P011.1.1 в локальных данных: окно записанной раннером сессии cd87b321 = 8 bash-вызовов, разрешаются 2 (bin/docflow), лишних 0, чтений tests/helpers.py нет; ни один ~/.claude/projects/-Users-bsv-Developer-bsv-docflow/*.jsonl не содержит `docflow prompt P011.1.1`. Цифра 601 = блоб 2405 байт на bbbfd76 = результат смоделированного чтения; фикс подтверждён синтетическим прогоном (RealHistoryP011Test) и размером реального блоба.
- tools/test-context-audits.py (14 тестов, ~4 с) не вызывается ни из gate (`go test ./...`), ни из CI (test-install, parity) — в Verify его запускают только P047.1.5 и Verify приёмки P047.2.1. Без docflow в PATH/bin тесты скипаются (bin/docflow подхватывается из этого checkout).
- context-audit на HEAD: skills:descriptions 628 токенов, рост 0/план за окно P035..HEAD (12 планов) → 628 при 200 планах, ok; start-reads 3958/4000 — запас 42 токена проекции; ни одна строка бюджета не падает. Ловушка P047.1.3 про README остаётся действующей.
- context-audit.py fixture_repo оставляет свой mkdtemp, если запись fixture-плана упала до rmtree (5 каталогов осталось на дофиксовом прогоне). Правка вне Do юнита.
- Unverified: Исходный случай P011.1.1 не подтверждён транскриптом: локального лога юнита с чтением tests/helpers.py нет, 601 токен подтверждён только синтетическим прогоном на реальной истории плюс размер блоба (2405 байт) — отсутствует проверка на настоящем транскрипте P011.1.1.
- Unverified: tools/test-context-audits.py не запускается ни одним гейтом и CI (gate = go test ./..., CI = test-install + parity) — отсутствует шаг CI, который гоняет `python3 tools/test-context-audits.py`; сегодня его выполняют только Verify юнита и Verify P047.2.1.
- Unverified: Глубокие glob-шаблоны: glob_match сопоставляет один компонент пути, поэтому `tools/**/*.py` теряет исторические файлы глубже одного уровня (дисковая сторона не затронута) — отсутствует фикстура юнита, читающего `tools/**/*.py`, где эпоха содержит tools/pkg/a.py без рабочего файла.
- Unverified: Регистронезависимость на Windows: деревная сторона глоба сопоставляет через fnmatchcase (чувствительно к регистру), дисковый glob.glob на Windows — нет — отсутствует прогон `python3 tools/test-context-audits.py` на windows-latest/Git Bash (пункт P047.1.2 про имя каталога транскриптов и git-обвязку, этот — про новый матчер).
- Unverified: Путь, именующий подмодуль (gitlink попадает в ls-tree -r --name-only), разрешается через tree и измеряется в 0 байт — отсутствует фикстура репозитория с подмодулем, который читает юнит.
- Unverified: RealHistoryP011Test зависит от достижимости bbbfd76 (git log --all): после нового сквоша история потеряет коммит и тест skipнется, синтетическая фикстура форму ловит, а реальный блоб нет — отсутствует копия истории P011 в фикстуре.

### [x] P047.1.6 Review the corrected context audit
Read:
- docs/design/format.md:L55-80
- tools/context-audit.py:L499-715
- tools/read-audit.py:L475-579
- tools/read-audit.py:L642-718
Do:
- Повторно проверить весь diff фазы из `## Phase diff` по Goal,
  Constraints, Decisions и Do всех реализационных юнитов, включая
  P047.1.5. Проверить сохранение решений при разбиении runner.md.
  Ничего не редактировать.
- Независимо воспроизвести оба дефекта из Handoff P047.1.4 на
  исправленном коде; проверить, что регрессии P047.1.5 действительно
  ловят исходные ошибки и не требуют личных логов. Отдельно проверить
  согласованность exit code, текста отказа и JSON `check` проекции.
- При дефекте вернуть `blocked` с `blocker: "plan"`; наблюдения без
  дефекта оставить обычными пунктами Handoff.
Verify: `go test ./...`
Depends: P047.1.1, P047.1.2, P047.1.3, P047.1.5
Role: review
Commit: P047.1.6: review corrected context audit
Handoff:
- Ревью P047.1.1–1.3 и 1.5 по Goal/Constraints/Decisions/Do: дефектов нет, оба дефекта из P047.1.4 исправлены. Проверено на macOS arm64, python3 из Xcode.
- Дефект 1 (проекция описаний скиллов): сценарий из tools/test-context-audits.py прогнан на версии 812350f (до фикса) — `--check` даёт exit 0 при проекции выше 1500 (ожидалось 1), падают test_check_fails_the_projected_budget и test_json_check_marks_the_descriptions_row_fail. На HEAD все 14 тестов проходят. На реальном репозитории при --plans 200/1000 и окнах P035/P040/P046 exit code текстового и JSON-прогона совпадают, `FAIL` в тексте совпадает с `check: fail` в JSON (в том числе FAIL по start-reads при --plans 1000).
- Дефект 2 (исторические пути): на реальной истории `resolve(root,'tests/helpers.py')` — версия 8423ec2 даёт None, HEAD с деревом коммита bbbfd76 даёт путь; блоб 2405 байт = 601 токен; `bash_reads('cat tests/helpers.py')` — старая версия [], новая [('tests/helpers.py', None)]. Регрессии на версии 8423ec2 падают (8 FAIL + 1 ERROR из 12; RealHistory пропущен без .git), на HEAD проходят 14/14. Тесты идут с HOME во временном каталоге и синтетическими логами; личные транскрипты не нужны. RealHistoryP011Test читает только git-историю репозитория и пропускается, если коммита нет.
- runner.md: тело файла идентично прежнему (без строк `## ` и пустых строк diff пуст), добавлены только 11 H2-заголовков; решения не изменены. Ссылки format.md:202,213 на #резервные-маршруты-и-таймаут целы. `bin/docflow lint`: ошибок нет, только 3 предупреждения «no fallback» плана P047.
- lint: предупреждение whole_read_limit не блокирует activate/thaw (activate останавливается только по hasErrors, thaw лишь печатает issues); закрытые юниты и замороженные планы не проверяются. tools/__pycache__ отсутствует. Полный `docflow verify P047.1.6` (go test ./...) прошёл, rc=0.
- Наблюдение (не дефект Do): в tools/context-audit.py `--check` сравнивает текущий размер с лимитом только для skills:descriptions. У start-reads и prompt:regular:ranged при отрицательном росте за окно (например `--window-plan P046` даёт tokens_at200 = -30284) проекция меньше текущего размера и может скрыть уже существующий пробой — тот же класс, что дефект 1. На дефолтном окне P035 рост положительный (2364 сейчас, 3958 при 200), текущий пробой не маскируется. Решение для P047.2.x: распространить правило «текущий размер тоже сверяется с лимитом» на остальные строки бюджета или зафиксировать, что отрицательное окно не поддерживается.
- Наблюдение: поле JSON называется `tokens_at200` даже при --plans N≠200, а проекция может быть отрицательной; на `check` не влияет.
- Наблюдение: в CHANGELOG.md нет пустой строки между добавленным блоком `### Added` и заголовком `## [0.16.1]`; поправить до релиза.
- Мой временный каталог /tmp/p047-review.u2iSh2 (экспорт HEAD с версиями инструментов до фикса) не удалён: rm с подстановкой команды заблокирован проверкой безопасности; он вне репозитория, можно удалить вручную.
- Unverified: Для start-reads и prompt:regular:ranged не проверен случай «текущий размер уже выше лимита, проекция убывает»: нет фикстуры с README больше start_budget, сжимающегося в окне роста, и ожидаемого exit 1.

## Phase 2: Acceptance

### [x] P047.2.1 Accept the context bounds
Read:
- tools/parity/README.md:L1-60
- tools/parity/scenarios/lint.sh
Do:
- One scenario per Goal statement through the built CLI:
  1. `python3 tools/context-audit.py --check` passes on this repository
     and prints the projection to 200 plans for every row.
  2. A parity scenario `tools/parity/scenarios/lint_whole_read.sh`: a
     plan unit reading a whole 20 KB file linked to two archive entries
     with Gotchas → `docflow lint` prints the warning with both numbers;
     the same unit with a line range → no warning; `whole_read_limit`
     raised above the file size → no warning.
  3. `python3 tools/read-audit.py --limit 5` prints per-unit rows and the
     `top` list.
- Запустить регрессии `tools/test-context-audits.py` из P047.1.5:
  прогнозируемый перерасход описаний даёт exit 1 и JSON `fail`, бюджетный
  контроль — exit 0 и `ok`; чтение удалённого исторического файла
  учитывается без его восстановления в рабочем дереве.
- Close every `Unverified:` item of earlier units with a test, or give it a
  disposition as the plan skill's Acceptance section requires.
Verify: `go build -o bin/docflow ./cmd/docflow && PYTHONDONTWRITEBYTECODE=1 PATH="$PWD/bin:$PATH" python3 tools/test-context-audits.py && bash tools/parity/run.sh bin/docflow bin/docflow && PATH="$PWD/bin:$PATH" python3 tools/context-audit.py --check`
Traps: A failing scenario is fixed inside this unit when the fix is small;
otherwise reply `blocked` with the reason.
Depends: P047.1.1, P047.1.2, P047.1.3, P047.1.4, P047.1.5, P047.1.6
Role: acceptance
Commit: P047.2.1: accept context bounds
Handoff:
- Цифры read-audit снова сдвинулись (полный прогон, rc=0, 18 с): 92 измеренных юнита, skipped 61 (раньше 1 — юниты Codex и `claude -p` без транскрипта теперь считаются, а не выпадают), лишние чтения 4895/6624 (73.9%), лишние токены 2211656/5233836 (42.3%). Рост чтений против Handoff P047.1.5 почти весь даёт раскрытие `~` в дисковом глобе: у P047.1.5 одна команда `grep -l … ~/.claude/projects/*.jsonl` = 326 файлов и +12474 токена (выходной лимит 50KB/4). `--limit 5` теперь включает skipped-юниты в последние N (P047.1.4 на Codex, P047.2.1 в работе), поэтому строк юнитов 3.
- Disposition Unverified, закрыто тестом: ветка отказа `--check` (P047.1.1) — test_check_fails_the_projected_budget, test_json_check_marks_the_descriptions_row_fail и новый test_start_reads_over_budget_now_fail_whatever_the_projection; юниты Codex (P047.1.2) — AuditableUnitsTest вместе с правкой отбора; табличный тест разбора shell (P047.1.2) — ShellReadsTest (21 строка); «текущий размер выше лимита, проекция убывает» (P047.1.6) — правило распространено на все строки бюджета, тест по start-reads.
- Accepted: точность проекции (P047.1.1) — проекция линейная и работает как сигнал тренда по окну после squash; контрольная точка появится только с закрытием следующего плана, ошибка меняет запас, а не направление сигнала. Accepted: переносимость git-обвязки и read-audit на Linux/Windows (P047.1.1, P047.1.2), регистр на Windows и подмодули (P047.1.5) — инструменты dev-only, их запускает мейнтейнер на macOS-checkout; в релизный бинарь и пользовательский путь они не входят, подмодулей в репозитории нет.
- Accepted: полный прогон read-audit вне Verify (P047.1.2) — прогнан здесь, rc=0; вход — локальные транскрипты, которых в CI нет, а падение разбора громкое (traceback, rc≠0), а не тихие цифры. Accepted: ветка `base_ctx changed` (P047.1.2) — это диагностическая строка, не пункт Goal. Accepted: лимит 50KB/2000 строк (P047.1.2) — константа модели (OUTPUT_CAP/LINES_CAP), её смена масштабирует все юниты одинаково.
- Accepted: парность README.ru (P047.1.3) — сверено: по 10 заголовков, 18 ссылок и 2 блока кода в каждом, цели ссылок совпадают, кроме взаимной ссылки. Accepted: Windows-путь (P047.1.3) — README сохраняет `%USERPROFILE%\.docflow` с junctions, путь `current\docflow.exe` следует из общей раскладки.
- Accepted: транскрипт P011.1.1 (P047.1.5) — его нет локально, поведение закреплено синтетическим HistoricalReadTest и RealHistoryP011Test. Accepted: test-context-audits.py не в CI (P047.1.5) — по решению плана регрессии вызывает Verify приёмки; это кандидат в Follow-ups при закрытии. Accepted: глубокие глобы `**` (P047.1.5) — обе стороны (tree и glob.glob) следуют семантике bash без globstar, рекурсия zsh лишь занижает редкий шаблон. Accepted: достижимость bbbfd76 (P047.1.5) — форму ловит синтетическая фикстура.
- Для закрытия: в CHANGELOG.md нет пустой строки перед `## [0.16.1]` (наблюдение P047.1.6) — я его не трогал, это выходит за Do; поправить при записи CHANGELOG. Инструменты tools/*-audit.py в docs/ и CHANGELOG не описаны, правила skipped и now-проверки записаны только в docstring инструментов. tools/__pycache__ отсутствует.
- Unverified: Ветка «now» для строки prompt:regular:ranged не имеет своей фикстуры: проверка общая с start-reads (один цикл), но нет теста, где промпт с диапазонами уже выше read_budget/2 при убывающей проекции.
- Unverified: Раскрытие `~` в глобе сделало видимыми команды `grep -l/-c/-q` по многим файлам: модель считает вывод grep как полный текст (до лимита 50KB на команду), хотя `-l` отдаёт только имена файлов. Нет теста и правила для флагов grep, ограничивающих вывод, поэтому такие команды завышают лишние токены (у P047.1.5 +12474).
