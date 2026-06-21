#!/bin/sh
set -eu

ssh_source_dir="${SSH_SOURCE_DIR:-/ssh}"
ssh_target_dir="/home/goproxy/.ssh"
config_source_file="${CONFIG_SOURCE_FILE:-/config-source/config.yml}"
config_target_file="${CONFIG_FILE:-/config/config.yml}"

mkdir -p /cache "$ssh_target_dir"

if [ -d "$ssh_source_dir" ]; then
	rm -rf "$ssh_target_dir"
	mkdir -p "$ssh_target_dir"
	cp -a "$ssh_source_dir"/. "$ssh_target_dir"/
fi

chown -R goproxy:goproxy /cache "$ssh_target_dir"
find "$ssh_target_dir" -type d -exec chmod 0700 {} +
find "$ssh_target_dir" -type f -name "*.pub" -exec chmod 0644 {} +
find "$ssh_target_dir" -type f ! -name "*.pub" -exec chmod 0600 {} +

if [ -n "$config_target_file" ] && [ -f "$config_source_file" ]; then
	mkdir -p "$(dirname "$config_target_file")"
	cp "$config_source_file" "$config_target_file"
	chown goproxy:goproxy "$config_target_file"
	chmod 0644 "$config_target_file"
fi

exec gosu goproxy:goproxy "$@"
