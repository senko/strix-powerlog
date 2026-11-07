#!/bin/sh
# Build first (as your user):  go build -trimpath -ldflags='-s -w' -o powerlog .
# Then:                         sudo ./install.sh
set -eu
cd "$(dirname "$0")"
[ "$(id -u)" = 0 ] || { echo "run with sudo" >&2; exit 1; }
[ -x powerlog ] || { echo "build the binary first: go build -trimpath -ldflags='-s -w' -o powerlog ." >&2; exit 1; }

id powerlog >/dev/null 2>&1 || useradd --system --no-create-home --home-dir /nonexistent \
    --shell /usr/sbin/nologin --comment "APU power logger" powerlog

install -m 0755 powerlog /usr/local/bin/powerlog
install -m 0644 powerlog.service /etc/systemd/system/powerlog.service
install -m 0644 powerlog.logrotate /etc/logrotate.d/powerlog

systemctl daemon-reload
systemctl enable powerlog.service
systemctl restart powerlog.service
sleep 3
systemctl --no-pager status powerlog.service | head -n 12
echo
tail -n 3 /var/log/powerlog/power.csv
