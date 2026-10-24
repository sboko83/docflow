---
docflow: 2
read_budget: 40000
start_read: README.md, docs/design/README.md
start_budget: 4000
---
## Queue

## Frozen

## Backlog
- `docflow run`: сверить классификатор отказов с живыми отказами Claude и
  Codex по лимиту, проверить возврат к основному исполнителю после сброса
  на живом прогоне; приёмку таймаута с detached-внуком прогнать на Linux CI
  ([P032](archive/P032-runner-fallback.md)).
- Релиз: проверить откат через `versions/` (переход `docflow update`
  0.7.0 → 0.8.0 проверен на macOS arm64) и живой `curl … | sh` на Linux
  (проверен на macOS arm64) ([P030](archive/P030-public-release.md)).
- `docflow run --close`: закрытие готового плана через агента
  (`/bsv-docflow-close`).
- Независимое ревью фазы или плана: юнит ревью другим ассистентом
  (исполняет codex — проверяет claude), находка — `blocked` + `blocker: plan`
  и круг replan, мелочь — `unverified`; ревьюер сам не правит. Не хватает
  базы диффа плана или фазы в `prompt`. Проба вручную — RMG P008; в P028
  ревью Claude по фазам нашло два дефекта ([P028](archive/P028-go-cutover.md)).
- `docflow run`: `## Prerequisites` проверяются при каждом запуске, а не
  один раз до первого юнита — предусловие, которое план сам нарушает
  посередине (parity в RMG P008 красный до P008.3.6), не даёт продолжить
  прогон. Решить: «только до старта» для части пунктов или lint-предупреждение
  о противоречии с Traps.
- Проверить глазами показ уведомления `docflow run` на macOS: `osascript` возвращает 0, но показ зависит от настроек уведомлений системы ([P025](archive/P025-autonomous-run.md)).
- Реальный юнит на Codex в Gradle/Xcode-проекте: эскалация записи вне
  репозитория, спиннер из живого потока, `--approve-for-me` после `resume`
  ([P015](archive/P015-multi-agent-runner.md)).
- RMG: прогон `prompt`/`verify`/`commit`/`run` с `repos: modules/*, projects/*`
  в его `docs/docflow.md` (P016 проверен только на временных репо,
  [P016](archive/P016-nested-repos-freeze.md)).
- Windows: `install.sh`, `docflow update`/`uninstall` (сейчас только macOS и
  Linux), `go test ./...` и паритет-сценарии на реальной машине — проверена
  только кросс-сборка ([P024](archive/P024-go-cli.md)). Паритет-сценарии
  на Linux не прогонялись ([P029](archive/P029-github-release.md)).
