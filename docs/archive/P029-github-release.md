---
id: P029
title: Релизы на GitHub, установщик и docflow update
closed: 2026-10-23
---
## Outcome

docflow ставится без Go: `curl … install.sh | sh` скачивает релизный архив,
сверяет сумму и запускает `docflow install` (раскладка `~/.docflow`, PATH,
скиллы агентам); есть `docflow update [--check]`, ежедневная проверка и
`uninstall`. Тег `vX.Y.Z` собирает релиз в GitHub Actions, дерево проходит
`tools/audit-public.sh`. Не проверены на GitHub: workflows `ci.yml` и
`release.yml`, живой `curl … | sh` и `docflow update` против github.com —
репозиторий ещё не опубликован.

## Decisions

- Готовые архивы в GitHub Releases и сетевой установщик — после P028 CLI один
  Go-бинарник, Go у пользователя не нужен; отвергнуто решение P020 (clone +
  сборка + симлинк в checkout): Go и checkout у каждого, обновление руками.
- Раскладка `~/.docflow/versions/<v>` + симлинк `current`, скиллы агентов
  ссылаются через `current` — атомарное обновление и откат, симлинки не
  переписываются; отвергнуто: `~/.local/share` + `~/.local/bin` (там
  dev-симлинк) и замена файлов на месте.
- Логика установки в Go (`docflow install`), `install.sh` только скачивает,
  сверяет и запускает — один тестируемый путь для install и update;
  отвергнуто: логика в shell.
- Последняя версия — ассет `latest/download/VERSION` — без лимита GitHub API,
  та же раскладка отдаётся `http.server` в тестах; отвергнуто: GitHub API и
  разбор редиректа `/releases/latest`.
- Ежедневная проверка обновлений только при stderr-терминале, таймаут 2 с,
  `DOCFLOW_NO_UPDATE_CHECK=1` — не засоряет вывод агентов и не тормозит.
- Версия — `var version` через `-ldflags -X`, тег сверяет отдельный
  `tools/check-release-tag.sh` с `docflow --version` — e2e собирает несколько
  версий из одного дерева, сверка проверяема локально; отвергнут inline-шаг
  в `release.yml`.
- Формат версии — голый SemVer `X.Y.Z` в ассете и `release.sh`; `install.sh`
  срезает ведущую `v` у `DOCFLOW_VERSION` — env пишут люди; отвергнута
  поддержка `v` в ассете.
- Публичный репозиторий — orphan-коммит этого дерева, прежняя история не
  публикуется; отвергнута отдельная папка `docflow-public` с ручной
  синхронизацией.
- Аудит публичного дерева — широкие паттерны плюс allow-файл
  `файл:паттерн`, доброкачественность решает человек; отвергнуто сужение
  regex (например, под RFC 2606-домены).
- `tools/test-install.sh` — герметичный PATH со заглушками агентов: настоящие
  `pi`/`claude`/`codex` и dev-docflow на машине разработчика маскировали
  заглушки; отвергнута заглушка в начале обычного PATH.
- Команды `Verify:`, `gate:` и `preflight:` идут в своей сессии без
  терминала, сигнал остановки убивает их группу — `zsh -i` в verify забрал
  терминал, и `docflow run` встал по SIGTTIN; отвергнут только `zsh -i +m`
  в тесте: следующая интерактивная команда повторила бы зависание.

## Gotchas

- Перед тегом `vX.Y.Z` поднять `var version` в `cmd/docflow/main.go`, иначе
  `release.yml` падает на `tools/check-release-tag.sh`.
- `tools/release.sh` на macOS: tar без `COPYFILE_DISABLE=1` кладёт в архив
  `._*`-файлы, а `.DS_Store` попадает в `skills/` — обе защиты в скрипте
  держать.
- `tools/test-install.sh` видит только перечисленные инструменты (`go`,
  `python3`, `curl`, shell, `zsh`) и системные каталоги: новый инструмент в
  `install.sh` или `release.sh` надо добавить в этот список.
- `install.sh` — POSIX `sh`; на macOS `/bin/sh` — bash и башизмы не ловит:
  прогонять и `DOCFLOW_TEST_SH=dash bash tools/test-install.sh`.
- Allow-строки `tools/audit-public.allow` привязаны к пути файла:
  переименование файла с находкой роняет аудит до правки allow.
- README и `docs/usage.md` зашивают URL `github.com/sboko83/docflow` — верны
  только после публикации.

## Follow-ups

- Публикация v0.4.0 с проверкой workflows и живого установщика на GitHub.
- Обновить правило Ruleblend `bsv-docflow-rules` из текущего `AGENTS.md`.
