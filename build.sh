#!/usr/bin/env bash
# 一键构建：NAS 服务端 + Android 客户端 + Windows 托盘 + 飞牛 fpk
# 用法：./build.sh [all|server|android|win|fnos]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${1:-all}"
OUT="$ROOT/dist"
mkdir -p "$OUT"

# 工程内自带的 Go 工具链优先，其次用系统 PATH 里的
if [ -x "$ROOT/../toolchain/go/bin/go" ]; then
  export PATH="$ROOT/../toolchain/go/bin:$PATH"
fi
export GOPATH="${GOPATH:-$ROOT/../toolchain/gopath}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export CGO_ENABLED=0

build_server() {
  echo "[build] 服务端 (linux/amd64)"
  (cd "$ROOT/server" && go build -trimpath -ldflags "-s -w" -o "$OUT/lanlink-server" .)
  echo "[build] 服务端 (linux/arm64)"
  (cd "$ROOT/server" && GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "$OUT/lanlink-server-arm64" .)
}

build_win() {
  echo "[build] Windows 托盘客户端 (windows/amd64)"
  (cd "$ROOT/client/windows" && GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -H windowsgui" -o "$OUT/lanlink-tray.exe" .)
}

build_fnos() {
  echo "[build] 飞牛 fpk"
  "$ROOT/deploy/fnos/build.sh"
}

build_android() {
  local sdk="${ANDROID_HOME:-$ROOT/../toolchain/android/sdk}"
  if [ ! -d "$sdk/platforms" ]; then
    echo "[build] 跳过 Android：未找到 SDK ($sdk)" >&2
    return 0
  fi
  echo "[build] Android APK"
  export ANDROID_HOME="$sdk" ANDROID_SDK_ROOT="$sdk"
  export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$ROOT/../toolchain/android/gradle-home}"
  printf 'sdk.dir=%s\n' "$sdk" > "$ROOT/client/android/local.properties"
  (cd "$ROOT/client/android" && chmod +x gradlew && ./gradlew --no-daemon -q assembleDebug)
  cp "$ROOT/client/android/app/build/outputs/apk/debug/app-debug.apk" "$OUT/lanlink-android.apk"
}

case "$TARGET" in
  all) build_server; build_android; build_win; build_fnos ;;
  server) build_server ;;
  android) build_android ;;
  win) build_win ;;
  fnos) build_fnos ;;
  *) echo "用法: $0 [all|server|android|win|fnos]" >&2; exit 1 ;;
esac

echo
echo "产物："
ls -lh "$OUT" | sed '1d'
