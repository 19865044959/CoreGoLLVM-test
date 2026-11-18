#!/usr/bin/env python3
"""扫描 stress/*/scenario.md，生成 docs/STRESS_MATRIX.md。"""
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STRESS = os.path.join(ROOT, "stress")
OUT = os.path.join(ROOT, "docs", "STRESS_MATRIX.md")


def parse(path):
    text = open(path, encoding="utf-8").read()
    m = re.match(r"^---\n(.*?)\n---\n", text, re.S)
    meta = {}
    if not m:
        return meta
    key = None
    for line in m.group(1).splitlines():
        if ":" not in line:
            continue
        k, v = line.split(":", 1)
        key, v = k.strip(), v.strip()
        if v.startswith("[") and v.endswith("]"):
            meta[key] = [x.strip() for x in v[1:-1].split(",") if x.strip()]
        else:
            meta[key] = v
    return meta


rows = []
for name in sorted(os.listdir(STRESS)):
    path = os.path.join(STRESS, name, "scenario.md")
    if not os.path.isfile(path):
        continue
    meta = parse(path)
    rows.append((name, meta))

lines = [
    "# stress 压测矩阵（自动生成）",
    "",
    "> 由 `tools/gen_stress_matrix.py` 扫描 `stress/*/scenario.md` 生成；不占 19 维 unit 配额。",
    "",
    "| 场景 | 优先级 | 覆盖维度 | requires | 默认时长 | max_workers |",
    "|---|---|---|---|---:|---:|",
]
for name, m in rows:
    covers = ", ".join(m.get("covers", [])) if isinstance(m.get("covers"), list) else m.get("covers", "")
    req = ", ".join(m.get("requires", [])) if isinstance(m.get("requires"), list) else m.get("requires", "")
    lines.append(
        f"| {name} | {m.get('priority', '')} | {covers} | {req} | "
        f"{m.get('default_seconds', '')} | {m.get('max_workers', '')} |"
    )
lines += [
    "",
    "## 覆盖维度计数",
    "",
]
counts = {}
for _, m in rows:
    covers = m.get("covers", [])
    if not isinstance(covers, list):
        covers = [covers]
    for c in covers:
        if c:
            counts[c] = counts.get(c, 0) + 1
for dim in sorted(counts):
    lines.append(f"- {dim}: {counts[dim]}")
lines += ["", f"共 {len(rows)} 个 stress 场景。", ""]

open(OUT, "w", encoding="utf-8").write("\n".join(lines))
print(f"wrote {OUT}: {len(rows)} scenarios")
