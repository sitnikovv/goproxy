# Private Go Module Proxy

HTTP service for serving private Git modules through the GOPROXY protocol. The service fetches sources over SSH, caches
repositories, and serves versions, `go.mod` files, and zip archives in Go module proxy format.

Set values through `config.yml` or environment variables.

## Quick Start

1. Create a directory for the proxy SSH key:

```bash
mkdir -p ssh
chmod 700 ssh
KEY_FILE=<key-file-name>
cp /path/to/private/key "ssh/${KEY_FILE}"
chmod 600 "ssh/${KEY_FILE}"
```

If the key name differs or you need to set the user and port explicitly, add `ssh/config`:

```sshconfig
Host example.com
  User git
  Port 7999
  IdentityFile /home/goproxy/.ssh/<key-file-name>
  IdentitiesOnly yes
```

2. Create `config.yml` from the example and edit it for your Git server:

```bash
cp config.example.yml config.yml
```

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
```

3. Start the service:

```bash
HOST_UID=$(id -u) HOST_GID=$(id -g) docker compose up --build
```

After startup, the proxy is available on:

```text
http://localhost:8081
```

## Settings

| Env | Default |
| --- | --- |
| `LISTEN_ADDR` | `:8081` |
| `MODULE_PREFIX` | `example.com/project` |
| `SSH_PREFIX` | `ssh://git@example.com:7999/project` |
| `CACHE_DIR` | `/cache` |
| `GIT_SSH_COMMAND` | `ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/goproxy_known_hosts -o BatchMode=yes` |
| `MAPPING_FILE` | empty |
| `CONFIG_FILE` | empty |

`docker-compose.yml` sets:

```yaml
CONFIG_FILE: "/config/config.yml"
```

`config.yml` is mounted read-only into the container.

The `ssh` directory is mounted into the container as `/home/goproxy/.ssh:ro`. Put only the keys and SSH settings needed
by this proxy there.

For multiple module prefixes, add several pairs:

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
  - module_prefix: "modules.example.net/team"
    ssh_prefix: "ssh://git@modules.example.net:7999/team"
```

Each pair defines a separate `module_prefix` to `ssh_prefix` mapping rule.

For different SSH keys, use separate `Host` aliases in `ssh/config` and reference those aliases in `ssh_prefix`:

```sshconfig
Host project-a-git
  HostName example.com
  User git
  Port 7999
  IdentityFile /home/goproxy/.ssh/<project-a-key>
  IdentitiesOnly yes

Host project-b-git
  HostName example.com
  User git
  Port 7999
  IdentityFile /home/goproxy/.ssh/<project-b-key>
  IdentitiesOnly yes
```

```yaml
prefixes:
  - module_prefix: "example.com/project-a"
    ssh_prefix: "ssh://git@project-a-git:7999/project-a"
  - module_prefix: "example.com/project-b"
    ssh_prefix: "ssh://git@project-b-git:7999/project-b"
```
## Module Mapping

Default rule:

```text
example.com/project/<repo>
-> ssh://git@example.com:7999/project/<repo>.git
```

Module in a subdirectory:

```text
example.com/project/<repo>/<submodule>
-> ssh://git@example.com:7999/project/<repo>.git
-> module root: <submodule>
```

Example:

```text
example.com/project/sample-repo/sample-module@v1.1.1
-> ssh://git@example.com:7999/project/sample-repo.git
-> module root: sample-module
```

If the repository name in the module path ends with `.git`, the suffix is not added again:

```text
example.com/project/sample-fork.git
-> ssh://git@example.com:7999/project/sample-fork.git
```

Use `MAPPING_FILE` for exceptions. Line format:

```text
<module>=<git-url>[,<subdir>]
```

Example:

```text
example.com/project/custom-module=ssh://git@example.com:7999/project/custom-repo.git,module-dir
```

## Go Setup

```bash
go env -w GOPROXY=http://localhost:8081,direct
go env -w GOPRIVATE=example.com
go env -w GONOPROXY=none
go env -w GONOSUMDB=example.com
```

Then:

```bash
go clean -modcache
go mod tidy
```

## Curl Checks

For module paths without uppercase letters, the URL path matches the module path:

```bash
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/list
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.info
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.mod
curl -L -o sample-module-v1.1.1.zip http://localhost:8081/example.com/project/sample-repo/sample-module/@v/v1.1.1.zip
curl -i http://localhost:8081/example.com/project/sample-repo/sample-module/@latest
```

If a module path contains uppercase letters, use `go`: it encodes `!` according to the Go proxy protocol.

## SSH Check

```bash
docker compose run --rm --entrypoint ssh goproxy \
  -o StrictHostKeyChecking=accept-new \
  -o UserKnownHostsFile=/tmp/goproxy_known_hosts \
  -o BatchMode=yes \
  -T -p 7999 git@example.com
```

## Diagnostics

Show HTTP requests made by `go`:

```bash
GODEBUG=cmdgohttplog=1 go mod tidy
```

Check Go settings:

```bash
go env GOPROXY GOPRIVATE GONOPROXY GONOSUMDB
```

Verbose SSH check inside the container:

```bash
docker compose run --rm --entrypoint ssh goproxy \
  -o StrictHostKeyChecking=accept-new \
  -o UserKnownHostsFile=/tmp/goproxy_known_hosts \
  -o BatchMode=yes \
  -vvv -T -p 7999 git@example.com
```

Inspect cached mirror repositories:

```bash
docker compose exec goproxy sh -lc 'find /cache -maxdepth 1 -name "*.git" -print'
docker compose exec goproxy sh -lc 'git -C /cache/<hash>.git tag -l'
```

For modules in subdirectories, the service accepts tags like `v1.1.1` and `<submodule>/v1.1.1`. If no semver tags are
available, `@latest` returns a pseudo-version from `HEAD`.
