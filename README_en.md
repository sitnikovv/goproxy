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

2. Create `config.yml` from the example and edit it for your Git server:

```bash
cp config.example.yml config.yml
```

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
    key_file: "<key-file-name>"
```

3. Start the service:

```bash
docker compose up --build
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

Put `config.yml` next to `docker-compose.yml`. Put SSH keys in the `ssh` directory.

`key_file` sets the file name in the `ssh` directory. For `key_file: "project-key"`, the file must exist as
`ssh/project-key`.

For rootless Podman, use the same files. Replace `docker compose` with `podman compose` in commands if the Podman
compose plugin is installed.

For multiple module prefixes, add several pairs:

```yaml
prefixes:
  - module_prefix: "example.com/project"
    ssh_prefix: "ssh://git@example.com:7999/project"
    key_file: "project-key"
  - module_prefix: "modules.example.net/team"
    ssh_prefix: "ssh://git@modules.example.net:7999/team"
    key_file: "team-key"
```

Each pair defines a separate `module_prefix` to `ssh_prefix` mapping rule. If several prefixes use the same Git host,
each prefix can set its own `key_file`.

If the container cannot resolve a Git host through DNS, add an IP for it:

```yaml
hosts:
  - host: "example.com"
    ip: "192.0.2.10"
    insecure: true
```

`insecure: true` disables SSH host key checking only for that host. If the parameter is omitted or set to `false`, the
service uses the regular `accept-new` mode.

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

For a `prefixes` item with `key_file`, the service creates alias `goproxy-prefix-N`, where `N` is the item's list
position.

```bash
docker compose exec --user goproxy goproxy ssh -F /cache/ssh_config -T goproxy-prefix-1
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
docker compose exec --user goproxy goproxy ssh -F /cache/ssh_config -vvv -T goproxy-prefix-1
```

Inspect cached mirror repositories:

```bash
docker compose exec --user goproxy goproxy sh -lc 'find /cache -maxdepth 1 -name "*.git" -print'
docker compose exec --user goproxy goproxy sh -lc 'git -C /cache/<hash>.git tag -l'
```

For modules in subdirectories, the service accepts tags like `v1.1.1` and `<submodule>/v1.1.1`. If no semver tags are
available, `@latest` returns a pseudo-version from `HEAD`.

### Common Errors

If `go` connects to the Git host directly:

```text
unrecognized import path "example.com/project/repo": reading https://example.com/project/repo?go-get=1
```

Check `GONOPROXY`. For private modules that must go through this proxy, the value must be `none`:

```bash
go env GOPROXY GOPRIVATE GONOPROXY GONOSUMDB
go env -w GONOPROXY=none
```

If a public module is not found with `direct`:

```text
reading github.com/vendor/module/go.mod at revision v1.2.3: unknown revision v1.2.3
```

Add the public Go proxy to `GOPROXY`:

```bash
go env -w GOPROXY=http://localhost:8081,https://proxy.golang.org,direct
```

If `go.sum` contains an old checksum for a private module:

```text
checksum mismatch
```

Remove only the lines for the affected private module and version, then recalculate dependencies:

```bash
module_path="example.com/project/repo/v2"
version="v2.0.0"
sed -i "\#^${module_path//\//\\/} ${version}\\(/go\\.mod\\)\\? #d" go.sum
go clean -modcache
go mod tidy
```
