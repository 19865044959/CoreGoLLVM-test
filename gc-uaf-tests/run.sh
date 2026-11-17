#!/usr/bin/env bash
# CoreGoLLVM GC/栈映射 UAF 用例运行器。配置全部来自环境变量，见 README.md。
#
#   ./run.sh                 # 跑全部用例
#   ./run.sh p0_09           # 只跑名字含 p0_09 的用例
#   ./run.sh list            # 列出用例
#   ./run.sh clean           # 清空 build/
#
# 关键环境变量：
#   TOOLCHAIN=go|goc|stock   go=打了 -go-abi 补丁的 go 驱动；goc=llvm-goc；stock=标准 go
#   GO / GOC / ORACLE_GO     对应工具路径
#   ABI=gc|gollvm            默认 gc
#   PROFILES=base,clobberfree,efence,stress
#   REPEAT=3  TIMEOUT=120  FILTER=  ORACLE=0  KEEP_GOING=1
#   BUILD_FLAGS="..."        透传给编译器的额外参数（如 -O0 相关开关）
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BUILD=${BUILD_DIR:-$HERE/build}
TESTS_DIR=$HERE/tests

TOOLCHAIN=${TOOLCHAIN:-go}
GO=${GO:-go}
GOC=${GOC:-llvm-goc}
ORACLE_GO=${ORACLE_GO:-go}
ABI=${ABI:-gc}
BUILD_FLAGS=${BUILD_FLAGS:-}
PROFILES=${PROFILES:-base,clobberfree,efence,stress}
REPEAT=${REPEAT:-3}
TIMEOUT=${TIMEOUT:-120}
FILTER=${FILTER:-}
ORACLE=${ORACLE:-0}
KEEP_GOING=${KEEP_GOING:-1}

usage() {
	sed -n '2,17p' "$0"
}

case "${1:-}" in
-h | --help)
	usage
	exit 0
	;;
list)
	for d in "$TESTS_DIR"/*/; do
		[ -f "$d/main.go" ] && basename "$d"
	done
	exit 0
	;;
clean)
	rm -rf "$BUILD"
	echo "cleaned $BUILD"
	exit 0
	;;
"") ;;
*) FILTER=$1 ;;
esac

mkdir -p "$BUILD"

profile_env() {
	case "$1" in
	base) printf 'GOGC=100' ;;
	clobberfree) printf 'GOGC=100 GODEBUG=clobberfree=1' ;;
	efence) printf 'GOGC=100 GODEBUG=efence=1' ;;
	stress) printf 'GOGC=1 GODEBUG=clobberfree=1' ;;
	*) printf 'GOGC=100 GODEBUG=%s' "$1" ;; # 允许 PROFILES 里写裸 GODEBUG 项
	esac
}

build_test() { # name out
	local name=$1 out=$2 log="$BUILD/$1.build.log"
	case "$TOOLCHAIN" in
	goc)
		$GOC -fgo-abi="$ABI" -enable-gc=1 $BUILD_FLAGS -o "$out" "$TESTS_DIR/$name/main.go" >"$log" 2>&1
		;;
	stock)
		$GO build $BUILD_FLAGS -o "$out" "$TESTS_DIR/$name/main.go" >"$log" 2>&1
		;;
	*)
		$GO build -go-abi="$ABI" $BUILD_FLAGS -o "$out" "$TESTS_DIR/$name/main.go" >"$log" 2>&1
		;;
	esac
}

build_oracle() { # name out
	local name=$1 out=$2 log="$BUILD/$1.oracle.build.log"
	$ORACLE_GO build $BUILD_FLAGS -o "$out" "$TESTS_DIR/$name/main.go" >"$log" 2>&1
}

names=()
for d in "$TESTS_DIR"/*/; do
	[ -f "$d/main.go" ] || continue
	name=$(basename "$d")
	if [ -n "$FILTER" ]; then
		case "$name" in
		*"$FILTER"*) ;;
		*) continue ;;
		esac
	fi
	names+=("$name")
done

if [ ${#names[@]} -eq 0 ]; then
	echo "no tests match filter '$FILTER'"
	exit 2
fi

echo "== CoreGoLLVM GC/stackmap UAF tests =="
echo "toolchain=$TOOLCHAIN abi=$ABI profiles=$PROFILES repeat=$REPEAT oracle=$ORACLE"
echo "tests: ${#names[@]}"
echo

pass=0
fail=0
failed_names=()

for name in "${names[@]}"; do
	bin="$BUILD/$name"
	if ! build_test "$name" "$bin"; then
		echo "[BUILD-FAIL] $name  (log: $BUILD/$name.build.log)"
		tail -n 10 "$BUILD/$name.build.log" | sed 's/^/    /'
		fail=$((fail + 1))
		failed_names+=("$name(build)")
		[ "$KEEP_GOING" = 1 ] || break
		continue
	fi

	obin=""
	if [ "$ORACLE" = 1 ]; then
		obin="$BUILD/$name.oracle"
		if ! build_oracle "$name" "$obin"; then
			echo "[ORACLE-BUILD-FAIL] $name  (log: $BUILD/$name.oracle.build.log)"
			obin=""
		fi
	fi

	ok=1
	for prof in ${PROFILES//,/ }; do
		if [ -f "$TESTS_DIR/$name/.skip-envs" ] && grep -qx -- "$prof" "$TESTS_DIR/$name/.skip-envs"; then
			echo "  [skip] $name profile=$prof"
			continue
		fi
		envs=$(profile_env "$prof")
		for r in $(seq 1 "$REPEAT"); do
			log="$BUILD/$name.$prof.$r.log"
			# shellcheck disable=SC2086
			env $envs timeout "${TIMEOUT}s" "$bin" >"$log" 2>&1
			rc=$?
			if [ "$rc" -ne 0 ]; then
				printf '[FAIL] %s profile=%s run=%s exit=%s\n' "$name" "$prof" "$r" "$rc"
				tail -n 8 "$log" | sed 's/^/    /'
				ok=0
				break 2
			fi
			if ! grep -qx "ALL PASS $name" "$log"; then
				printf '[FAIL] %s profile=%s run=%s (missing pass marker)\n' "$name" "$prof" "$r"
				tail -n 8 "$log" | sed 's/^/    /'
				ok=0
				break 2
			fi
			if [ -n "$obin" ]; then
				olog="$BUILD/$name.$prof.$r.oracle.log"
				# shellcheck disable=SC2086
				env $envs timeout "${TIMEOUT}s" "$obin" >"$olog" 2>&1
				orc=$?
				if [ "$orc" -ne 0 ]; then
					printf '[WARN] %s oracle run failed (profile=%s rc=%s)\n' "$name" "$prof" "$orc"
				elif ! diff -q "$log" "$olog" >/dev/null; then
					printf '[FAIL] %s oracle output mismatch (profile=%s run=%s)\n' "$name" "$prof" "$r"
					diff "$olog" "$log" | head -n 10 | sed 's/^/    /'
					ok=0
					break 2
				fi
			fi
		done
	done

	if [ "$ok" -eq 1 ]; then
		echo "[PASS] $name"
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
		failed_names+=("$name")
		[ "$KEEP_GOING" = 1 ] || break
	fi
done

echo
echo "== summary: $pass passed, $fail failed =="
if [ "$fail" -ne 0 ]; then
	echo "failed: ${failed_names[*]}"
	echo "logs: $BUILD/"
	exit 1
fi
