---
docflow: 3
read_budget: 40000
start_read: README.md, docs/design/README.md
start_budget: 4000
---
## Queue
- P046
- P047

## Frozen

## Backlog
- Проверить PTY-сценарии живости на Linux и кадры/спиннер в настоящей
  консоли Windows: время блока, квота used/left, сбросы, перенос на 30
  колонках, спиннер и активный кап закрытия; убедиться, что квота
  не выходит за рамку, а обрезка спиннера сохраняет её
  ([P045](archive/P045-run-liveness-output.md)).
- Проверить сценарий `run_unverified` в CI на ubuntu-latest и
  windows-latest: Git Bash, `parity_shim claude`, вызов `docflow.exe prompt`
  из fake-агента; локально проверена только macOS arm64
  ([P044](archive/P044-unverified-discipline.md)).
- Проверить в настоящей консоли Windows вопрос старта о human-блоке:
  `y` и заметка снимают блок и прогон продолжается; перенаправленный stdin,
  `--yes` и блок `plan` не спрашивают. Запустить `TestIsTerminalWindows*` и
  `TestAskUnblockWindowsConsoleTyped` с `DOCFLOW_WINDOWS_CONSOLE_ASK=1`
  ([P041](archive/P041-human-unblock.md)).
- Проверить на живом агенте, что юнит после `docflow unblock` сначала
  проверяет условие блока командой, а при невыполненном условии снова
  блокируется с `human` — при случае на рабочем прогоне
  ([P041](archive/P041-human-unblock.md)).
- Перенести Unix shell fixtures `internal/runner` и `internal/agentio`
  на Windows, где они пока пропускаются
  ([P040](archive/P040-windows-support.md)).
- Проверить релизную установку, обновление, ссылки скиллов и удаление
  на Windows без Developer Mode и прав администратора
  ([P040](archive/P040-windows-support.md)).
- `docflow run`: сверить классификатор отказов с живыми отказами Claude и
  Codex по лимиту, проверить возврат к основному исполнителю после сброса
  на живом прогоне — при случае на рабочем прогоне, подписку ради проверки
  не тратить ([P032](archive/P032-runner-fallback.md)).
- Бенчмарк накладных расходов docflow: эталонный план из юнитов разной
  тяжести на временном репо, прогон на маршрутах ростера; мерить стену,
  время модели, инструментов и гейта, токены (proxy промпта против
  фактических), долю ревью и replan, качество (гейт, дефекты ревью).
  Данные уже есть в `<git-dir>/docflow/run/*.out` (у pi — `durationMs` на
  вызов модели и инструмента), `<run-id>.events.jsonl` (`unit_end` —
  секунды и стоимость юнита, [P038](archive/P038-runner-backlog-batch.md))
  и `docflow stats`.
- GLM на pi медленный: в P036 юнит 7–33 мин, 70–90 % стены — ожидание
  модели (30–84 вызова, ~50 ток/с, reasoning ~65 % вывода, вызовы > 60 с —
  до 47 % времени модели), остальное — `go test ./...`. Сравнить
  `thinking max` и `high` на одном юните (часть бенчмарка).
- Политика расхода подписок: порог окна (`claude 7d ≥ 30%` — маршрут
  недоступен, переход дальше по `fallback:`) и бюджет API на прогон.
  Отступление от P021 — личная настройка влияет на выбор маршрута в
  прогоне — записать решением. Опирается на живую проверку классификатора
  отказов; квоту pi раннер не опрашивает.
- Интерактивный режим как опция: живой прогон (юниты, поток текущего
  агента, квоты), план целиком, подробный вывод, активация и закрытие
  плана. До плана решить: отдельный бинарь `docflow-tui` (зависимости
  разрешены, [P042](archive/P042-dependency-policy.md)) или `docflow ui`
  на `net/http` (SSE). Конфиги правятся в `$EDITOR` + `lint`. Поток
  событий уже есть
  ([P038](archive/P038-runner-backlog-batch.md)).
- Прогнать `docflow run --close` с живым агентом по `bsv-docflow-close` на
  реальном плане — проверено только на fake-агенте
  ([P038](archive/P038-runner-backlog-batch.md)).
- Codex: на живом Xcode/Gradle-прогоне проверить эскалацию записи вне
  репозитория (DerivedData, кэши), эскалацию после `resume` и зафиксировать
  запрос эскалации Verify самим агентом; сохранённые логи нужных
  свидетельств не содержат
  ([P015](archive/P015-multi-agent-runner.md),
  [P039](archive/P039-e2e-and-live-checks.md)).
