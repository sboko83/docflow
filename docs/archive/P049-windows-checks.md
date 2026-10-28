---
id: P049
title: Windows checks of console and live runner rules
closed: 2026-10-28
---
## Outcome
Консольные ветки Windows воспроизводятся через ConPTY, повторная проверка
после `unblock` и закрытие плана — через живые smoke-сценарии.
Закрыты Windows-пункты [P041](P041-human-unblock.md),
[P045](P045-run-liveness-output.md), [P048](P048-windows-fixtures.md)
и живое закрытие реального плана из [P038](P038-runner-backlog-batch.md).
P049 закрыт агентом через `docflow run --close`.

## Decisions
- Живые проверки — флагами `--unblock`/`--close` в `tools/smoke-run.sh`,
  а не отдельным скриптом: сценарии рядом с существующим живым прогоном,
  квота тратится только по явному флагу — отвергнут скрипт
  `tools/live-check-*`.
- ConPTY-тест вместо ручной проверки с `DOCFLOW_WINDOWS_CONSOLE_ASK=1`:
  тест не требует человека и входит в `go test ./...` — отвергнут ручной
  режим у консоли.
- Закрытие плана живым агентом через `docflow run --close`: закрывающий
  прогон проверяется настоящим планом — отвергнут ручной `docflow close`
  как единственная проверка закрывающего пути.
- `smoke-run.sh` снимает `DOCFLOW_RUNNER_UNIT` для всех своих вызовов:
  сценарий работает и из гейта раннера — отвергнута очистка окружения
  только в команде Verify одного плана.
- Приёмка принимает успешный живой прогон закрытия из P049.2.1, если
  проверенный код не менялся: повтор требует вызовов zai сверх бюджета
  плана — отвергнут обязательный повтор `--close` в приёмке.

## Gotchas
- Любой новый флаг сценария в `tools/smoke-run.sh` обязан сохранять
  снятие `DOCFLOW_RUNNER_UNIT` в начале скрипта: из сессии агента внутри
  `docflow run` ручные `docflow block/unblock/finish/done/commit`
  отвергаются с кодом 5. Ассерты живых веток говорят текстом плана
  (`### [!] P001.1.1`, счётчики строк, коды выхода) и никогда —
  формулировками агента.
- Закрывающий промпт от `closeCLICommand` на Windows даёт синтаксис
  PowerShell (`& 'C:\...\docflow.exe' ...`), а pi-агенты на Windows
  водят Git Bash: живой агент должен адаптировать команду сам. Если
  живое закрытие застревает на `& '...'`, искать здесь.
- ConPTY-тест устроен как два процесса: хост переисполняет тестовый
  бинарь (`DOCFLOW_TEST_CONSOLE_STAGE=host`, `-test.run`) и препечатает
  ответ ребёнку сразу после запуска — typed-ahead безопасен, только пока
  вопрос является первым чтением stdin. Хендлы хоста ребёнку менять
  нельзя, ребёнок получает их от ConPTY.
- Точный вывод `docflow run` закреплён в `terminal_accept_test.go` и
  `unblock_acceptance_test.go`; правка вывода требует правки обоих.
  Фразы правил из `cycleText`/`RunnerCycle` ассерты сравнивают после
  нормализации пробелов (`strings.Fields`); правка `RunnerCycle`
  перепинивает хеш в `TestStrictRunnerSchema`.

## Follow-ups
- Accepted: повторяемость живого закрывающего прогона не проверяется
  повторным `bash tools/smoke-run.sh --close` — каждый прогон тратит два
  вызова zai, а состояния отказа, fallback и нумерации логов закреплены
  `TestRunCloseFallback`, `TestRunCloseRefusalExhausted`,
  `TestRunCloseRetryKeepsLogs`.
- Waits: CI или машина на Linux и macOS — прогнать `go test ./...`,
  `tools/test-install.sh` и parity на этих ОС; цель P049 ограничена
  Windows ([P048](P048-windows-fixtures.md)).
