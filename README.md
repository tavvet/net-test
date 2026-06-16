# net-test

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![CI](https://github.com/tavvet/net-test/actions/workflows/ci.yml/badge.svg)](https://github.com/tavvet/net-test/actions/workflows/ci.yml)
![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Windows-blue)

Консольная (TUI) утилита для проверки качества интернет-соединения: **задержка,
потери, джиттер, где именно на маршруте начинаются обрывы, диагноз по зонам сети
и реальная скорость (download/upload)** — всё в одном окне терминала. Плюс
one-shot режим (`--once`) с текстовым или JSON-отчётом для тикетов и cron.

Написана на Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea). Весь
ICMP — на чистом Go через unprivileged-сокеты (**не нужен `sudo`** на macOS/Linux)
или Win32-API `iphlpapi` на Windows. Системные `ping`/`traceroute` не вызываются.

## Возможности

| Вкладка | Что показывает |
|---|---|
| **1 · Пинг** | Живой монитор до цели: текущий/средний RTT, потери %, джиттер (RFC 3550), спарклайн истории (`✕` = потеря) и вердикт качества в 4 уровня (Отлично / Хорошо / Плохо / Критично) с причиной. Вердикт считается по **скользящему окну** последних проб, а не за всю сессию — одна ранняя потеря не «залипает» на минуты. |
| **2 · Маршрут** | mtr-стиль: пингует каждый хоп и показывает per-hop потери, last/avg/best/worst, СКО и AS-имя сети. **Автоматически помечает `⚠` проблемный узел** — но только если потери/задержка держатся до конца маршрута (одиночный спайк от rate-limit ICMP игнорируется). Хосты и ASN резолвятся в фоне. |
| **3 · Диагноз** | Маршрут разбивается на зоны (локальная сеть → провайдер → транзит → назначение) по ASN, и для каждой выводится статус. Одна строка-вердикт: «маршрут здоров» или «проблема в зоне «X»». |
| **4 · Скорость** | Реальный замер через открытые endpoint'ы Cloudflare: латентность, download ↓ и upload ↑ в Mbps, плюс датацентр CF и ваш IP. |

## Установка

```bash
# через Go (нужен Go 1.26+)
go install github.com/tavvet/net-test@latest

# или из исходников
git clone git@github.com:tavvet/net-test.git
cd net-test
make build            # → ./net-test
```

Готовые бинарники под mac/linux/windows — на странице [Releases](https://github.com/tavvet/net-test/releases)
(или соберите сами: `make dist`, см. [Сборка](#сборка)).

## Запуск

```bash
./net-test                                   # цель по умолчанию — 1.1.1.1
./net-test -target 8.8.8.8
./net-test -target google.com -interval 500ms -max-hops 20
```

Без сборки: `go run .` (или `make run ARGS="-target 8.8.8.8"`).

### Флаги

| Флаг | По умолчанию | Описание |
|---|---|---|
| `-target` | `1.1.1.1` | хост или IP для проверки |
| `-interval` | `1s` | интервал между пробами (пинг и трассировка) |
| `-timeout` | `2s` | таймаут ожидания ICMP-ответа |
| `-max-hops` | `30` | максимум хопов для трассировки |
| `-once` | — | один прогон без TUI: отчёт в stdout и выход |
| `-duration` | `10s` | окно сбора данных для `-once` |
| `-json` | — | JSON-формат отчёта (только с `-once`) |
| `-no-speed` | — | пропустить тест скорости в `-once` |
| `-version` | — | показать версию и выйти |

### Управление в TUI

| Клавиша | Действие |
|---|---|
| `1` `2` `3` `4` / `Tab` / `←` `→` | переключение вкладок |
| `s` | запустить (перезапустить) тест скорости |
| `q` / `Ctrl-C` | выход |

### One-shot отчёт (для тикетов провайдеру / cron)

```bash
./net-test -once -target 8.8.8.8                       # текстовый отчёт
./net-test -once -duration 30s -no-speed               # без speed-теста
./net-test -once -json | jq '.trace.diagnosis'         # JSON в пайплайн
```

Текстовый отчёт читаемый «как есть», можно прикладывать к тикету ISP.
JSON удобен для cron-мониторинга: формат стабильный, длительности в `*_ms`,
поля документированы в [internal/report/report.go](internal/report/report.go).

## Как выглядит

Вкладка «Маршрут» — проблемный хоп помечен `⚠` (потери держатся до конца),
рядом с IP видно AS-имя сети, а в «Сред» — прирост задержки `+ΔX`
(в реальном терминале — с цветовой подсветкой):

```
 net-test    цель 1.1.1.1  │  CF MXP IT  │  время 0:42
  1 Пинг    2 Маршрут    3 Диагноз    4 Скорость

     #  Хост / IP                            Потери  Отпр  Послед     Сред   Лучш   Худш    СКО
     1  router.lan (10.0.1.1) · локальная сеть   0%    42    1.2      1.4    0.9    3.1    0.4
     2  5.180.172.2 · ITNET-AS                   0%    42   12.0     11.8   10.1   30.2    5.1
     3  212.237.216.242 · ITNET-AS              18%    42   85.1     42.1   14.0  190.3   38.2
  ⚠  4  162.158.236.14 · CLOUDFLARENET          18%    42   52.3  42.1 +30   14.0  190.3   38.2
  ⚠  5  one.one.one.one (1.1.1.1) · CLOUDFLARENET 18%  42   53.0     44.0   12.0   61.0    9.3

  1-4/⭾ вкладки   s тест скорости   q выход
```

Вкладка «Диагноз» сводит маршрут в зоны и говорит, где проблема:

```
Маршрут до 1.1.1.1
Состояние: проблема в зоне «CLOUDFLARENET»

✓  Локальная сеть              хоп 1
✓  Провайдер ITNET-AS          хопы 2-3
⚠  CLOUDFLARENET               хопы 4-5
   → потери 18% начиная с хопа 4
```

## Платформы

| Платформа | Статус |
|---|---|
| macOS (arm64/amd64) | ✅ собрано и проверено на реальной сети |
| Linux (amd64/arm64) | ✅ тот же unprivileged-путь, что и macOS |
| Windows (amd64/386) | ✅ бэкенд на `iphlpapi` (без админки), проверено на реальной Windows |
| Android | 🔭 пакет `internal/probe` готов к `gomobile bind` (нужен нативный UI) |

Подробности — в [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Сборка

```bash
make build                  # нативный бинарник → ./net-test
make dist                   # все платформы → dist/
make dist VERSION=1.0.0     # вшить версию (видна в `net-test -version`)
make checksums              # sha256 → dist/SHA256SUMS
make help                   # все цели
```

Кросс-сборка статическая (`CGO_ENABLED=0`), со стрипом (`-s -w`) и `-trimpath`.

## Разработка

```bash
make test          # юнит-тесты (без сети)
make test-race     # с детектором гонок
make live          # живой сетевой тест (пинг+трасса)
make fmt vet       # форматирование и статанализ
```

Как устроен код и как добавить платформенный бэкенд — см.
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) и [CONTRIBUTING.md](CONTRIBUTING.md).
Планы развития — в [docs/ROADMAP.md](docs/ROADMAP.md).

## Ограничения

- IPv4 (ICMPv4). IPv6 пока не поддерживается.
- Латентность на вкладке «Скорость» — это HTTP/TLS до ближайшего датацентра
  Cloudflare, а не чистый сетевой RTT (для точного RTT — вкладка «Пинг»).
- Длина маршрута фиксируется на цели после первого успешного ответа; смена
  маршрута на лету не отслеживается.

## Лицензия

[MIT](LICENSE) © 2026 Anton Rudakov
