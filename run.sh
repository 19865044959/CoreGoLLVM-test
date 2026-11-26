#!/usr/bin/env bash
# CoreGoVLLM-test 运行器（unit 语义用例 + stress 长跑场景）。详见 README.md。
#
#   ./run.sh                     # 跑 unit 用例（默认）
#   SUITE=stress ./run.sh        # 跑 stress/ 长跑场景
#   SUITE=all ./run.sh           # 两套都跑
#   ./run.sh L05                 # 只跑某个 unit 维度
#   ./run.sh l05_pool            # 按名字子串过滤
#   ./run.sh list                # 列出用例/场景
#   ./run.sh clean               # 清空 build/
#
# 关键环境变量：
#   SUITE=unit|stress|all     默认 unit；stress 场景在 stress/<name>/main.go
#   TOOLCHAIN=go|goc|stock    go=打了 -go-abi 补丁的 go 驱动；goc=llvm-goc；stock=标准 go
#   GO / GOC / ORACLE_GO      对应工具路径（标准 go 可配 GOTOOLCHAIN=go1.24.1）
#   ABI=gc|gollvm             默认 gc
#   DIMS=L05,L17              维度过滤（仅 unit；逗号分隔）
#   PRIORITY=P0,P1            优先级过滤（读 case.md/scenario.md 的 priority）
#   PROFILES=base,clobberfree,efence,stress,k8s / soak,soak-single,soak-noasync
#   REPEAT=3                  每个 profile 重复次数；第 r 次注入 UAF_SEED=（SEED_BASE+r-1）
#   STRESS_SECONDS=60         透传给 stress 场景的时长；TIMEOUT 默认 = STRESS_SECONDS+120
#   STRESS_HEARTBEAT=5 STRESS_WORKERS=...  透传给 stress 场景
#   CAPS="generics,templates" 只运行 requires 全在这个集合内的用例；为空则不过滤
#   TIMEOUT=120  FILTER=  ORACLE=0  KEEP_GOING=1  SEED_BASE=1
#   BUILD_ONLY=1              只编译不运行（asm/cgo/交叉编译/stress 构建检查）
#   BUILD_FLAGS="..."         透传给编译器的额外参数
#   用例目录含 BUILD_MODE（内容 package/module）时按目录构建，支持构建标签/多包：
#     module -> (cd 用例目录 && go build ... .)
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BUILD=${BUILD_DIR:-$HERE/build}
CASES_DIR=${CASES_DIR:-$HERE/cases}
STRESS_DIR=${STRESS_DIR:-$HERE/stress}

TOOLCHAIN=${TOOLCHAIN:-go}
GO=${GO:-go}
GOC=${GOC:-llvm-goc}
ORACLE_GO=${ORACLE_GO:-go}
ABI=${ABI:-gc}
BUILD_FLAGS=${BUILD_FLAGS:-}
SUITE=${SUITE:-unit}
STRESS_SECONDS=${STRESS_SECONDS:-60}
STRESS_HEARTBEAT=${STRESS_HEARTBEAT:-5}
if [ -z "${PROFILES+x}" ]; then
	if [ "$SUITE" = stress ]; then PROFILES=soak; else PROFILES=base,clobberfree,efence,stress,k8s; fi
fi
REPEAT=${REPEAT:-3}
if [ -z "${TIMEOUT+x}" ]; then
	if [ "$SUITE" = stress ]; then TIMEOUT=$((STRESS_SECONDS + 120)); else TIMEOUT=120; fi
fi
FILTER=${FILTER:-}
DIMS=${DIMS:-}
PRIORITY=${PRIORITY:-}
CAPS=${CAPS:-}
ORACLE=${ORACLE:-0}
KEEP_GOING=${KEEP_GOING:-1}
SEED_BASE=${SEED_BASE:-1}
BUILD_ONLY=${BUILD_ONLY:-0}

usage() { sed -n '2,32p' "$0"; }

case "${1:-}" in
-h | --help)
	usage
	exit 0
	;;
clean)
	rm -rf "$BUILD"
	echo "cleaned $BUILD"
	exit 0
	;;
esac

mkdir -p "$BUILD"

profile_env() {
	case "$1" in
	base) printf 'GOGC=100' ;;
	clobberfree) printf 'GOGC=100 GODEBUG=clobberfree=1' ;;
	efence) printf 'GOGC=100 GODEBUG=efence=1' ;;
	stress) printf 'GOGC=1 GODEBUG=clobberfree=1' ;;
	k8s) printf 'GOGC=1 GOMAXPROCS=2 GOMEMLIMIT=256MiB GODEBUG=clobberfree=1,asyncpreemptoff=0' ;;
	single) printf 'GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1' ;;
	noasync) printf 'GOGC=1 GODEBUG=clobberfree=1,asyncpreemptoff=1' ;;
	soak) printf 'GOGC=1 GOMAXPROCS=2 GOMEMLIMIT=256MiB GODEBUG=clobberfree=1,asyncpreemptoff=0 GOTRACEBACK=crash' ;;
	window) printf 'GOGC=off GOMAXPROCS=1 GODEBUG=gcstoptheworld=2,gccheckmark=1,clobberfree=1 GOTRACEBACK=crash' ;;
	detector) printf 'GOGC=100 GOMAXPROCS=1 GODEBUG=efence=1 GOTRACEBACK=crash' ;;
	finale) printf 'GOGC=1 GOMAXPROCS=2 GODEBUG=checkfinalizers=1,clobberfree=1 GOTRACEBACK=crash' ;;
	layout) printf 'GOGC=1 GOMAXPROCS=2 GODEBUG=gcshrinkstackoff=1,clobberfree=1 GOTRACEBACK=crash' ;;
	soak-single) printf 'GOGC=1 GOMAXPROCS=1 GOMEMLIMIT=256MiB GODEBUG=clobberfree=1 GOTRACEBACK=crash' ;;
	soak-noasync) printf 'GOGC=1 GOMAXPROCS=2 GOMEMLIMIT=256MiB GODEBUG=clobberfree=1,asyncpreemptoff=1 GOTRACEBACK=crash' ;;
	*) printf 'GOGC=100 GODEBUG=%s' "$1" ;;
	esac
}

meta_file() { # cdir: case.md 或 scenario.md
	if [ -f "$1/case.md" ]; then
		printf '%s' "$1/case.md"
	elif [ -f "$1/scenario.md" ]; then
		printf '%s' "$1/scenario.md"
	fi
}

case_priority() { # cdir
	local f
	f=$(meta_file "$1")
	[ -n "$f" ] || return 0
	sed -n 's/^priority:[[:space:]]*//p' "$f" | head -1
}

case_requires() { # cdir
	local f
	f=$(meta_file "$1")
	[ -n "$f" ] || return 0
	sed -n 's/^requires:[[:space:]]*\[\(.*\)\].*/\1/p' "$f" | head -1
}

in_list() { # needle csv
	[ -z "$2" ] && return 0
	case ",$2," in *",$1,"*) return 0 ;; *) return 1 ;; esac
}

# ---------- discover ----------
entries=()
if [ "$SUITE" = unit ] || [ "$SUITE" = all ]; then
	for d in "$CASES_DIR"/*/; do
		[ -d "$d" ] || continue
		dim=$(basename "$d")
		case "$dim" in L[0-9][0-9]_*) ;; *) continue ;; esac
		if [ -n "$DIMS" ]; then
			dmatch=0
			for want in $(echo "$DIMS" | tr ',' ' '); do
				case "$dim" in *"$want"*) dmatch=1 ;; esac
			done
			[ "$dmatch" = 1 ] || continue
		fi
		for c in "$d"*/; do
			[ -d "$c" ] || continue
			[ -f "$c/main.go" ] || continue
			name=$(basename "$c")
			[ -n "$FILTER" ] && case "$name" in *"$FILTER"*) ;; *) continue ;; esac
			if [ -n "$PRIORITY" ]; then
				p=$(case_priority "$c")
				[ -n "$p" ] && in_list "$p" "$PRIORITY" || [ -z "$p" ] || continue
			fi
			if [ -n "$CAPS" ]; then
				req=$(case_requires "$c")
				if [ -n "$req" ]; then
					skip=0
					for tag in $(echo "$req" | tr ',' ' '); do
						[ -z "$tag" ] && continue
						in_list "$tag" "$CAPS" || skip=1
					done
					[ "$skip" = 1 ] && continue
				fi
			fi
			entries+=("unit|$dim|$name")
		done
	done
fi

if [ "$SUITE" = stress ] || [ "$SUITE" = all ]; then
	if [ -d "$STRESS_DIR" ]; then
		for c in "$STRESS_DIR"/*/; do
			[ -d "$c" ] || continue
			[ -f "$c/main.go" ] || continue
			name=$(basename "$c")
			[ -n "$FILTER" ] && case "$name" in *"$FILTER"*) ;; *) continue ;; esac
			if [ -n "$PRIORITY" ]; then
				p=$(case_priority "$c")
				[ -n "$p" ] && in_list "$p" "$PRIORITY" || [ -z "$p" ] || continue
			fi
			if [ -n "$CAPS" ]; then
				req=$(case_requires "$c")
				if [ -n "$req" ]; then
					skip=0
					for tag in $(echo "$req" | tr ',' ' '); do
						[ -z "$tag" ] && continue
						in_list "$tag" "$CAPS" || skip=1
					done
					[ "$skip" = 1 ] && continue
				fi
			fi
			entries+=("stress|-|$name")
		done
	fi
fi

if [ "${1:-}" = "list" ]; then
	for e in "${entries[@]}"; do
		IFS='|' read -r suite dim name <<<"$e"
		if [ "$suite" = stress ]; then
			printf '%-7s %-28s %-40s %s\n' "$suite" "-" "$name" "$(case_priority "$STRESS_DIR/$name")"
		else
			printf '%-7s %-28s %-40s %s\n' "$suite" "$dim" "$name" "$(case_priority "$CASES_DIR/$dim/$name")"
		fi
	done
	exit 0
fi

if [ ${#entries[@]} -eq 0 ]; then
	echo "no cases match (SUITE='$SUITE' DIMS='$DIMS' FILTER='$FILTER' PRIORITY='$PRIORITY')"
	exit 2
fi


echo "== CoreGoVLLM-test =="
echo "suite=$SUITE toolchain=$TOOLCHAIN abi=$ABI profiles=$PROFILES repeat=$REPEAT oracle=$ORACLE timeout=${TIMEOUT}s"
[ "$SUITE" = stress ] || [ "$SUITE" = all ] && echo "stress_seconds=$STRESS_SECONDS heartbeat=${STRESS_HEARTBEAT}s"
echo "cases: ${#entries[@]}"
echo

build_case() { # cdir out
	local cdir=$1 out=$2
	local name
	name=$(basename "$cdir")
	local log="$BUILD/$(basename "$(dirname "$cdir")")__$name.build.log"
	local mode=files
	[ -f "$cdir/BUILD_MODE" ] && mode=$(tr -d '[:space:]' <"$cdir/BUILD_MODE")
	if [ "$mode" = module ]; then
		case "$TOOLCHAIN" in
		goc) (cd "$cdir" && $GOC -fgo-abi="$ABI" -enable-gc=1 $BUILD_FLAGS -o "$out" .) >"$log" 2>&1 ;;
		stock) (cd "$cdir" && $GO build $BUILD_FLAGS -o "$out" .) >"$log" 2>&1 ;;
		*) (cd "$cdir" && $GO build -go-abi="$ABI" $BUILD_FLAGS -o "$out" .) >"$log" 2>&1 ;;
		esac
		return $?
	fi
	local files=()
	shopt -s nullglob
	for f in "$cdir"/*.go "$cdir"/*.s; do
		files+=("$f")
	done
	shopt -u nullglob
	case "$TOOLCHAIN" in
	goc) $GOC -fgo-abi="$ABI" -enable-gc=1 $BUILD_FLAGS -o "$out" "${files[@]}" >"$log" 2>&1 ;;
	stock) $GO build $BUILD_FLAGS -o "$out" "${files[@]}" >"$log" 2>&1 ;;
	*) $GO build -go-abi="$ABI" $BUILD_FLAGS -o "$out" "${files[@]}" >"$log" 2>&1 ;;
	esac
}

build_oracle() { # cdir out
	local cdir=$1 out=$2
	local name
	name=$(basename "$cdir")
	local log="$BUILD/$(basename "$(dirname "$cdir")")__$name.oracle.build.log"
	local mode=files
	[ -f "$cdir/BUILD_MODE" ] && mode=$(tr -d '[:space:]' <"$cdir/BUILD_MODE")
	if [ "$mode" = module ]; then
		(cd "$cdir" && $ORACLE_GO build $BUILD_FLAGS -o "$out" .) >"$log" 2>&1
		return $?
	fi
	local files=()
	shopt -s nullglob
	for f in "$cdir"/*.go "$cdir"/*.s; do
		files+=("$f")
	done
	shopt -u nullglob
	$ORACLE_GO build $BUILD_FLAGS -o "$out" "${files[@]}" >"$log" 2>&1
}

declare -A dim_pass dim_fail
pass=0
fail=0
failed=()

for e in "${entries[@]}"; do
	IFS='|' read -r suite dim name <<<"$e"
	if [ "$suite" = stress ]; then
		cdir="$STRESS_DIR/$name"
		key="stress/$name"
		dim="stress"
	else
		cdir="$CASES_DIR/$dim/$name"
		key="$dim/$(basename "$cdir")"
	fi
	outdir="$BUILD/$dim"
	mkdir -p "$outdir"
	bin="$outdir/$name"

	if ! build_case "$cdir" "$bin"; then
		echo "[BUILD-FAIL] $key (log: $BUILD/${dim}__$name.build.log)"
		tail -n 10 "$BUILD/${dim}__$name.build.log" | sed 's/^/    /'
		fail=$((fail + 1))
		dim_fail[$dim]=$((${dim_fail[$dim]:-0} + 1))
		failed+=("$key(build)")
		[ "$KEEP_GOING" = 1 ] || break
		continue
	fi

	if [ "$BUILD_ONLY" = 1 ]; then
		echo "[BUILD-OK] $key"
		pass=$((pass + 1))
		dim_pass[$dim]=$((${dim_pass[$dim]:-0} + 1))
		continue
	fi

	obin=""
	if [ "$ORACLE" = 1 ] && [ "$suite" = unit ]; then
		obin="$outdir/$name.oracle"
		build_oracle "$cdir" "$obin" || obin=""
	fi

	extra=""
	[ -f "$cdir/.env" ] && extra=$(grep -v '^[[:space:]]*#' "$cdir/.env" | tr '\n' ' ')

	marker="ALL PASS $name"
	[ "$suite" = stress ] && marker="STRESS PASS $name"

	ok=1
	for prof in ${PROFILES//,/ }; do
		if [ -f "$cdir/.skip-envs" ] && grep -qx -- "$prof" "$cdir/.skip-envs"; then
			continue
		fi
		envs=$(profile_env "$prof")
		for r in $(seq 1 "$REPEAT"); do
			seed=$((SEED_BASE + r - 1))
			log="$outdir/${name}.${prof}.${r}.log"
			runenvs="$envs $extra UAF_SEED=$seed"
			crashfile=""
			if [ "$suite" = stress ]; then
				crashfile="$outdir/${name}.${prof}.${r}.crash.txt"
				: >"$crashfile"
				runenvs="$runenvs STRESS_CRASH_FILE=$crashfile"
			fi
			# 独立 cwd：core（若产生）落在 build 目录内
			# shellcheck disable=SC2086
			(cd "$outdir" && env $runenvs timeout -s QUIT -k 30 "${TIMEOUT}s" "$bin") >"$log" 2>&1
			rc=$?
			if [ "$rc" -ne 0 ]; then
				printf '[FAIL] %s profile=%s seed=%s exit=%s\n' "$key" "$prof" "$seed" "$rc"
				tail -n 8 "$log" | sed 's/^/    /'
				[ -n "$crashfile" ] && [ -s "$crashfile" ] && echo "    crash-output: $crashfile"
				for cf in "$outdir"/core*; do [ -e "$cf" ] && { echo "    core: $cf"; break; }; done
				ok=0
				break 2
			fi
			if ! grep -qx "$marker" "$log"; then
				printf '[FAIL] %s profile=%s seed=%s (missing pass marker: %s)\n' "$key" "$prof" "$seed" "$marker"
				tail -n 8 "$log" | sed 's/^/    /'
				[ -n "$crashfile" ] && [ -s "$crashfile" ] && echo "    crash-output: $crashfile"
				for cf in "$outdir"/core*; do [ -e "$cf" ] && { echo "    core: $cf"; break; }; done
				ok=0
				break 2
			fi
			if [ "$suite" = unit ] && [ -f "$cdir/.expected" ] && ! diff -q "$cdir/.expected" "$log" >/dev/null; then
				printf '[FAIL] %s profile=%s seed=%s (golden output mismatch)\n' "$key" "$prof" "$seed"
				diff "$cdir/.expected" "$log" | head -n 10 | sed 's/^/    /'
				ok=0
				break 2
			fi
			if [ -n "$obin" ]; then
				olog="$outdir/${name}.${prof}.${r}.oracle.log"
				# shellcheck disable=SC2086
				(cd "$outdir" && env $envs $extra UAF_SEED=$seed timeout -s QUIT -k 30 "${TIMEOUT}s" "$obin") >"$olog" 2>&1
				if [ $? -ne 0 ]; then
					printf '[WARN] %s oracle failed profile=%s\n' "$key" "$prof"
				elif ! diff -q "$log" "$olog" >/dev/null; then
					printf '[FAIL] %s oracle mismatch profile=%s seed=%s\n' "$key" "$prof" "$seed"
					diff "$olog" "$log" | head -n 10 | sed 's/^/    /'
					ok=0
					break 2
				fi
			fi
		done
	done

	if [ "$ok" -eq 1 ]; then
		echo "[PASS] $key"
		pass=$((pass + 1))
		dim_pass[$dim]=$((${dim_pass[$dim]:-0} + 1))
	else
		fail=$((fail + 1))
		dim_fail[$dim]=$((${dim_fail[$dim]:-0} + 1))
		failed+=("$key")
		[ "$KEEP_GOING" = 1 ] || break
	fi
done

echo
echo "== summary: $pass passed, $fail failed =="
for dim in $(printf '%s\n' "${!dim_pass[@]}" "${!dim_fail[@]}" | sort -u); do
	printf '  %-28s pass=%s fail=%s\n' "$dim" "${dim_pass[$dim]:-0}" "${dim_fail[$dim]:-0}"
done
if [ "$fail" -ne 0 ]; then
	echo "failed: ${failed[*]}"
	echo "logs: $BUILD/"
	exit 1
fi
