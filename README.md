# Private Go Module Proxy

English version: [README_en.md](README_en.md).

HTTP-сервис, через который Go получает приватные модули из Git по GOPROXY protocol. Сервис забирает исходники по SSH,
кеширует репозитории и отдает версии, `go.mod` и zip-архивы в формате Go module proxy.

Значения задаются через `config.yml` или переменные окружения.

## Быстрый старт

1. Создайте каталог для SSH-ключа proxy:

```bash
mkdir -p ssh
chmod 700 ssh
KEY_FILE=<key-file-name>
cp /path/to/private/key "ssh/${KEY_FILE}"
chmod 600 "ssh/${KEY_FILE}"
```

2. Создайте `config.yml` из примера и отредактируйте его под свой Git-сервер:

```bash
cp config.example.yml config.yml
```

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
    key_file: "<key-file-name>"
```

3. Запустите сервис:

```bash
HOST_UID=$(id -u) HOST_GID=$(id -g) docker compose up --build
```

После запуска proxy доступен на порту:

```text
http://localhost:8081
```

## Настройки

| Env | Default |
| --- | --- |
| `LISTEN_ADDR` | `:8081` |
| `MODULE_PREFIX` | `example.com/project` |
| `SSH_PREFIX` | `ssh://git@example.com:7999/project` |
| `CACHE_DIR` | `/cache` |
| `GIT_SSH_COMMAND` | `ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/goproxy_known_hosts -o BatchMode=yes` |
| `MAPPING_FILE` | пусто |
| `CONFIG_FILE` | пусто |

В `docker-compose.yml` задано:

```yaml
CONFIG_FILE: "/config/config.yml"
```

Файл `config.yml` монтируется в контейнер read-only.

Каталог `ssh` монтируется в контейнер как `/home/goproxy/.ssh:ro`. Кладите в него только ключи, которые нужны этому
proxy.

`key_file` задается относительно каталога `ssh`. Для `key_file: "project-key"` файл должен лежать в `ssh/project-key`.
Если используется `key_file`, оставьте `GIT_SSH_COMMAND` стандартным.

Для нескольких префиксов модулей добавьте несколько пар:

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
    key_file: "project-key"
  - module_prefix: "modules.example.net/team"
    ssh_prefix: "ssh://git@modules.example.net:7999/team"
    key_file: "team-key"
```

Каждая пара задает отдельное правило маппинга `module_prefix` → `ssh_prefix`. Если у нескольких префиксов один Git
host, для каждого префикса можно указать свой `key_file`.

Если DNS внутри контейнера не видит Git host, добавьте для него IP:

```yaml
hosts:
  - host: "example.com"
    ip: "192.0.2.10"
    insecure: true
```

`insecure: true` отключает проверку SSH host key только для этого host. Если параметр не указан или равен `false`,
сервис использует обычный режим `accept-new`.

## Маппинг модулей

Базовое правило:

```text
example.com/project/<repo>
→ ssh://git@example.com:7999/project/<repo>.git
```

Модуль в подкаталоге:

```text
example.com/project/<repo>/<submodule>
→ ssh://git@example.com:7999/project/<repo>.git
→ module root: <submodule>
```

Например:

```text
example.com/project/sample-repo/sample-module@v1.1.1
→ ssh://git@example.com:7999/project/sample-repo.git
→ module root: sample-module
```

Если имя репозитория в module path заканчивается на `.git`, суффикс повторно не добавляется:

```text
example.com/project/sample-fork.git
→ ssh://git@example.com:7999/project/sample-fork.git
```

Для исключений используйте `MAPPING_FILE`. Формат строки:

```text
<module>=<git-url>[,<subdir>]
```

Пример:

```text
example.com/project/custom-module=ssh://git@example.com:7999/project/custom-repo.git,module-dir
```

## Настройка Go

```bash
go env -w GOPROXY=http://localhost:8081,direct
go env -w GOPRIVATE=example.com
go env -w GONOPROXY=none
go env -w GONOSUMDB=example.com
```

Затем:

```bash
go clean -modcache
go mod tidy
```

## Проверка через curl

Для путей без заглавных букв URL совпадает с module path:

```bash
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/list
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.info
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.mod
curl -L -o sample-module-v1.1.1.zip http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.zip
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@latest
```

Если в module path есть заглавные буквы, выполняйте запрос через `go`: команда сама кодирует `!` по правилам Go proxy
protocol.

## Проверка SSH

Для элемента `prefixes` с `key_file` сервис создает alias `goproxy-prefix-N`, где `N` — позиция элемента в списке.

```bash
docker compose exec goproxy ssh -F /cache/ssh_config -T goproxy-prefix-1
```

## Диагностика

Посмотреть, какие HTTP-запросы делает `go`:

```bash
GODEBUG=cmdgohttplog=1 go mod tidy
```

Проверить настройки Go:

```bash
go env GOPROXY GOPRIVATE GONOPROXY GONOSUMDB
```

Подробная проверка SSH внутри контейнера:

```bash
docker compose exec goproxy ssh -F /cache/ssh_config -vvv -T goproxy-prefix-1
```

Проверить кешированные mirror-репозитории:

```bash
docker compose exec goproxy sh -lc 'find /cache -maxdepth 1 -name "*.git" -print'
docker compose exec goproxy sh -lc 'git -C /cache/<hash>.git tag -l'
```

Если версия находится в подкаталоге, сервис принимает теги вида `v1.1.1` и `<submodule>/v1.1.1`. Если semver-тегов
нет, `@latest` возвращает pseudo-version от `HEAD`.
