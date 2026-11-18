#!/usr/bin/env bash
# stress 夜间跑批：默认 6 个场景 × soak profile × 1 小时，串行。
# 用法：
#   ./stress/run_nightly.sh
#   STRESS_SECONDS=1800 PROFILES=soak,soak-single FILTER=s04 ./stress/run_nightly.sh
#   TOOLCHAIN=go ABI=gc ./stress/run_nightly.sh   # 内网 CoreGoLLVM
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
STRESS_SECONDS=${STRESS_SECONDS:-3600}
PROFILES=${PROFILES:-soak}
REPEAT=${REPEAT:-1}
FILTER=${FILTER:-}
KEEP_GOING=${KEEP_GOING:-1}
TIMEOUT=${TIMEOUT:-$((STRESS_SECONDS + 300))}
STAMP=${STAMP:-$(date +%Y%m%d-%H%M%S)}
BUILD_DIR=${BUILD_DIR:-$HERE/build/nightly-$STAMP}

export SUITE=stress STRESS_SECONDS PROFILES REPEAT TIMEOUT KEEP_GOING BUILD_DIR

echo "== stress nightly stamp=$STAMP seconds=$STRESS_SECONDS profiles=$PROFILES repeat=$REPEAT filter=${FILTER:-all}"
echo "== build_dir=$BUILD_DIR toolchain=${TOOLCHAIN:-go} abi=${ABI:-gc}"
echo "== start=$(date '+%F %T')"

# FILTER 作为可选位置参数传给 run.sh（空格分隔时不支持多词，请用单值）。
# shellcheck disable=SC2086
"$HERE/run.sh" ${FILTER}
rc=$?

echo "== end=$(date '+%F %T') rc=$rc"
echo "== logs: $BUILD_DIR/stress/"
if [ "$rc" -ne 0 ]; then
	echo "== 失败产物（crash/core）:"
	find "$BUILD_DIR/stress" -maxdepth 1 \( -name '*.crash.txt' -o -name 'core*' \) -size +0c 2>/dev/null | head -20
fi
exit "$rc"
