#!/usr/bin/env bash
# 拉取三方库的测试代码（partial clone，只 checkout *_test.go / testdata / go.mod）。
# 用法：tools/fetch_lib.sh <name> <repo-url> <tag>
# 结果放在 research/repos/<name>，不进入 git。
set -eu

if [ $# -ne 3 ]; then
	echo "usage: $0 <name> <repo-url> <tag>" >&2
	exit 2
fi

NAME=$1
URL=$2
TAG=$3
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DEST="$ROOT/research/repos/$NAME"

HOST_IP=$(grep -m1 '^nameserver' /etc/resolv.conf | awk '{print $2}')
export http_proxy="http://$HOST_IP:7890"
export https_proxy="http://$HOST_IP:7890"
export all_proxy="http://$HOST_IP:7890"

if [ -d "$DEST/.git" ]; then
	echo "exists: $DEST"
	exit 0
fi

mkdir -p "$ROOT/research/repos"
git clone --depth 1 --filter=blob:none --sparse --branch "$TAG" "$URL" "$DEST" >/dev/null 2>&1
(
	cd "$DEST"
	git sparse-checkout set --no-cone '**/*_test.go' '**/testdata/**' 'go.mod' 'README.md'
)
echo "fetched $NAME@$TAG -> $DEST"
