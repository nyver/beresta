#!/bin/sh
set -eu

application=${1:?usage: measure-idle-pi.sh /path/to/beresta-server}
test "$(uname -s)" = "Linux"
case "$(uname -m)" in
    aarch64|arm64) ;;
    *) echo "Raspberry Pi acceptance requires Linux arm64" >&2; exit 2 ;;
esac

data_root=$(mktemp -d)
cleanup() {
    if [ -n "${server_pid:-}" ]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf -- "$data_root"
}
trap cleanup EXIT INT TERM

cat >"$data_root/config.yaml" <<'EOF'
server:
  listen: "127.0.0.1:18443"
backups:
  enabled: false
EOF

"$application" --data "$data_root" &
server_pid=$!
i=0
while [ "$i" -lt 30 ]; do
    if curl --silent --fail --insecure https://127.0.0.1:18443/health >/dev/null; then
        break
    fi
    kill -0 "$server_pid"
    i=$((i + 1))
    sleep 1
done
curl --silent --fail --insecure https://127.0.0.1:18443/health >/dev/null

sleep 60
rss_kib=$(ps -o rss= -p "$server_pid" | tr -d ' ')
clock_ticks=$(getconf CLK_TCK)
start_ticks=$(awk '{ print $14 + $15 }' "/proc/$server_pid/stat")
sample_seconds=10
sleep "$sample_seconds"
end_ticks=$(awk '{ print $14 + $15 }' "/proc/$server_pid/stat")
cpu_percent=$(awk -v start="$start_ticks" -v end="$end_ticks" -v hz="$clock_ticks" -v seconds="$sample_seconds" 'BEGIN { printf "%.3f", ((end-start)/hz/seconds)*100 }')
echo "idle_rss_kib=$rss_kib idle_cpu_percent=$cpu_percent"
test "$rss_kib" -le 51200
awk -v cpu="$cpu_percent" 'BEGIN { exit !(cpu <= 1.0) }'
