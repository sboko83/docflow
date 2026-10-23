---
id: P007
title: Auto permission mode for the claude profile
closed: 2026-10-13
---
## Outcome
Встроенный профиль `claude` раннера работает в `--permission-mode auto`;
модели без поддержки `auto` получают явные права ключом профиля
`model_extra` (встроено для haiku).

## Decisions
- Профиль `claude` — `--permission-mode auto` без `--allowedTools`; haiku
  — `model_extra.haiku = ["--allowedTools", "Write", "Edit", "Bash"]` —
  классификатор `auto` проверяет команды агента на sonnet и opus, а haiku
  его не поддерживает и без явных прав получает отказ на любую запись —
  отвергнуто: `acceptEdits` + `--allowedTools Bash` для всех (Bash без
  проверки), `auto` + `--allowedTools Bash` для всех (то же самое),
  ярус `fast` → sonnet (нет дешёвого яруса). Заменяет решение о профиле
  `claude` из [P006](P006-runner.md).
- Ключ `model_extra` сопоставляется с моделью после перевода яруса, точным
  совпадением — предсказуемо, без угадывания по подстроке — отвергнуто:
  совпадение по подстроке id.

## Gotchas
- `model_extra` сопоставляется с id после перевода яруса: свой id быстрой
  модели в `models` требует своей строки в `model_extra`.
- Агент под раннером читает глобальный `~/.claude/CLAUDE.md` пользователя,
  включая правила автокоммита; от коммита держат раздел `Cycle (runner
  mode)` и проверка HEAD раннером (код 5).
- Smoke проверяет путь haiku + `model_extra`; чистый `auto` на sonnet
  проверен разовой пробой.

## Follow-ups
Нет.
