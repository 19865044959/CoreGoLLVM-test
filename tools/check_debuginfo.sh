#!/usr/bin/env bash
# 检查构建产物的 stack-map 符号与 DWARF 调试信息（L14/L17 辅助工具，不是 main.go 用例）。
# 用法：tools/check_debuginfo.sh [用例目录] [输出二进制]
#   GO=go GOTOOLCHAIN=go1.24.1 tools/check_debuginfo.sh cases/L14_debug_info/l14_traceback_frames /tmp/dbgbin
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
CASE=${1:-$ROOT/cases/L14_debug_info/l14_traceback_frames}
BIN=${2:-/tmp/gcuaf_debuginfo_bin}
GO=${GO:-go}
NM=${NM:-$GO tool nm}
READELF=${READELF:-readelf}

fails=0
check() { # desc, cmd...
	local desc=$1
	shift
	if "$@" >/dev/null 2>&1; then
		echo "[OK]   $desc"
	else
		echo "[FAIL] $desc"
		fails=$((fails + 1))
	fi
}

echo "== build: $CASE"
if [ -f "$CASE/go.mod" ]; then
	(cd "$CASE" && $GO build -gcflags="all=-N -l" -o "$BIN" .) || { echo "build failed"; exit 1; }
else
	(cd "$CASE" && $GO build -gcflags="all=-N -l" -o "$BIN" ./*.go) || { echo "build failed"; exit 1; }
fi

gclocals=$($NM "$BIN" 2>/dev/null | grep -cE 'gclocals|gcargs' || true)
echo "== stack-map symbols: $gclocals"
if [ "$gclocals" -gt 0 ]; then
	echo "[OK]   gclocals/gcargs present"
else
	# Go 1.24 的 go tool nm/objdump 不再稳定暴露 FUNCDATA 符号；此项降级为提示，
	# 栈映射正确性由 L17 运行级用例覆盖。
	echo "[WARN] stack-map symbols not exposed by this toolchain (covered by L17 runtime cases)"
fi

if command -v "$READELF" >/dev/null 2>&1; then
	sub=$($READELF --debug-dump=info "$BIN" 2>/dev/null | grep -c 'DW_TAG_subprogram' || true)
	echo "== DWARF subprograms: $sub"
	[ "$sub" -gt 0 ] && echo "[OK]   DW_TAG_subprogram present" || {
		echo "[FAIL] no DW_TAG_subprogram"
		fails=$((fails + 1))
	}
	lines=$($READELF --debug-dump=decodedline "$BIN" 2>/dev/null | grep -c 'main.go' || true)
	echo "== DWARF line entries for main.go: $lines"
	[ "$lines" -gt 0 ] && echo "[OK]   line table present" || {
		echo "[FAIL] no main.go line entries"
		fails=$((fails + 1))
	}
else
	echo "[WARN] readelf not found; skip DWARF checks"
fi

if [ "$fails" -ne 0 ]; then
	echo "== FAILED ($fails)"
	exit 1
fi
echo "== ALL OK"
