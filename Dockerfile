FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/goproxy .

FROM debian:bookworm-slim

ARG HOST_UID=1000
ARG HOST_GID=1000

RUN apt-get update \
    && apt-get install -y --no-install-recommends git openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${HOST_GID}" goproxy \
    && useradd --uid "${HOST_UID}" --gid "${HOST_GID}" --home-dir /home/goproxy --create-home --shell /usr/sbin/nologin goproxy

ENV LISTEN_ADDR=:8081 \
    MODULE_PREFIX=example.com/project \
    SSH_PREFIX=ssh://git@example.com:7999/project \
    CACHE_DIR=/cache \
    HOME=/home/goproxy \
    GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/tmp/goproxy_known_hosts -o BatchMode=yes" \
    GIT_TERMINAL_PROMPT=0

COPY --from=build /out/goproxy /out/goproxy

RUN mkdir -p /cache \
    && chown -R goproxy:goproxy /cache /home/goproxy /out/goproxy

VOLUME ["/cache"]
EXPOSE 8081

USER goproxy:goproxy
ENTRYPOINT ["/out/goproxy"]
