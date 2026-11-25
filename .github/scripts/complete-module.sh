#!/usr/bin/env sh
# 文件: .github/scripts/complete-module.sh
# 功能: 向已压缩模块内容追加管理器 APK 与 Android 平台辅助程序
# 用法: sh .github/scripts/complete-module.sh <模块包> <NetProxy.apk> <Main-dex> <Bootstrap-dex>
# 依赖: POSIX sh、7z、dirname、basename

set -eu

test "$#" -eq 4
module="$(CDPATH= cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")"
apk_dir="$(CDPATH= cd -- "$(dirname -- "$2")" && pwd)"
test "$(basename -- "$2")" = NetProxy.apk
test -s "$module"
test -s "$apk_dir/NetProxy.apk"
test -s "$3"
test -s "$4"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir "$stage/bin"
cp "$apk_dir/NetProxy.apk" "$stage/NetProxy.apk"
cp "$3" "$stage/bin/android-platform.dex"
cp "$4" "$stage/bin/android-bootstrap.dex"
(
  cd "$stage"
  7z a -tzip -mx=0 "$module" NetProxy.apk bin/android-platform.dex bin/android-bootstrap.dex
)
7z t "$module"
