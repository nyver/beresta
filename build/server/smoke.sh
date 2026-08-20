#!/bin/sh
set -eu

application=${1:?usage: smoke.sh /path/to/beresta-server}
data_root=$(mktemp -d)
server_pid=
cleanup() {
    if [ -n "$server_pid" ]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf -- "$data_root"
}
trap cleanup EXIT INT TERM

cat >"$data_root/config.yaml" <<'EOF'
server:
  listen: "127.0.0.1:18444"
backups:
  enabled: false
EOF

"$application" --data "$data_root" --init-only
test -f "$data_root/beresta.db"
test -f "$data_root/tls/server.crt"
test -f "$data_root/tls/server.key"
test -d "$data_root/blobs"
"$application" --data "$data_root" &
server_pid=$!
i=0
while [ "$i" -lt 30 ]; do
    if curl --silent --fail --insecure https://127.0.0.1:18444/health >/dev/null; then
        break
    fi
    kill -0 "$server_pid"
    i=$((i + 1))
    sleep 1
done
curl --silent --fail --insecure https://127.0.0.1:18444/health >/dev/null
kill "$server_pid"
wait "$server_pid"
server_pid=
"$application" --data "$data_root" verify
