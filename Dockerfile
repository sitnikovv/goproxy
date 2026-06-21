FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/goproxy .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends git openssh-client ca-certificates gosu \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 goproxy \
    && useradd --uid 1000 --gid 1000 --home-dir /home/goproxy --create-home --shell /usr/sbin/nologin goproxy

ENV LISTEN_ADDR=:8081 \
    MODULE_PREFIX=example.com/project \
    SSH_PREFIX=ssh://git@example.com:7999/project \
    CACHE_DIR=/cache \
    CONFIG_FILE=/config/config.yml \
    HOME=/home/goproxy \
    GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/goproxy_known_hosts -o BatchMode=yes" \
    GIT_TERMINAL_PROMPT=0

COPY --from=build /out/goproxy /out/goproxy
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN mkdir -p /cache \
    && chown -R goproxy:goproxy /cache /home/goproxy /out/goproxy \
    && chmod 0755 /usr/local/bin/docker-entrypoint.sh

VOLUME ["/cache"]
EXPOSE 8081

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/out/goproxy"]
