#!/usr/bin/env bash
# 在 NAS（Linux）上安装 LanLink 服务端并设为开机自启。需要 sudo。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${1:-$ROOT/dist/lanlink-server}"
DATA_DIR="${LANLINK_DATA:-/var/lib/lanlink}"
OPT_DIR="/opt/lanlink"
PORT="${LANLINK_PORT:-8787}"

if [ ! -f "$BIN" ]; then
  echo "找不到服务端二进制：$BIN，请先在仓库根目录执行 ./build.sh server" >&2
  exit 1
fi

if ! id lanlink >/dev/null 2>&1; then
  echo "[1/5] 创建系统用户 lanlink"
  sudo useradd --system --home "$DATA_DIR" --shell /usr/sbin/nologin lanlink
fi

echo "[2/5] 安装二进制到 $OPT_DIR"
sudo install -d -m 755 "$OPT_DIR"
sudo install -m 755 "$BIN" "$OPT_DIR/lanlink-server"

echo "[3/5] 准备数据目录 $DATA_DIR"
sudo install -d -m 750 -o lanlink -g lanlink "$DATA_DIR"

echo "[4/5] 安装 systemd 单元"
tmp="$(mktemp)"
sed "s#/opt/lanlink/lanlink-server#/opt/lanlink/lanlink-server#g; s#-addr :8787#-addr :$PORT#; s#-data /var/lib/lanlink#-data $DATA_DIR#" \
  "$ROOT/deploy/lanlink.service" > "$tmp"
sudo install -m 644 "$tmp" /etc/systemd/system/lanlink.service
rm -f "$tmp"
sudo systemctl daemon-reload
sudo systemctl enable --now lanlink

echo "[5/5] 状态"
sleep 1
sudo systemctl --no-pager --lines=15 status lanlink || true

echo
echo "完成。加入码与访问地址见上面的日志（journalctl -u lanlink -n 30 --no-pager）。"
