#!/usr/bin/env bash
# 打包飞牛（fnOS）应用：产出 dist/lanlink.fpk
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PKG="$ROOT/deploy/fnos/lanlink"

if [ -x "$ROOT/../toolchain/go/bin/go" ]; then
  export PATH="$ROOT/../toolchain/go/bin:$PATH"
fi
export CGO_ENABLED=0
export GOPATH="${GOPATH:-$ROOT/../toolchain/gopath}"

echo "[fnos] 编译服务端 (linux/amd64)"
mkdir -p "$PKG/app/server"
(cd "$ROOT/server" && go build -trimpath -ldflags "-s -w" -o "$PKG/app/server/lanlink-server" .)

cp "$ROOT/LICENSE" "$PKG/LICENSE"
chmod +x "$PKG"/cmd/*

echo "[fnos] fnpack build"
mkdir -p "$ROOT/dist"
(cd "$PKG" && fnpack build)
mv "$PKG/lanlink.fpk" "$ROOT/dist/lanlink.fpk"
ls -lh "$ROOT/dist/lanlink.fpk"
