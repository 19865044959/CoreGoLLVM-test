#!/usr/bin/env python3
"""扫描 cases/<dim>/<case>/case.md，生成 docs/MATRIX.md。"""
import os
import re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CASES = os.path.join(ROOT, "cases")
OUT = os.path.join(ROOT, "docs", "MATRIX.md")
TARGET = 50

DIMS = [
    ("L01_generics", "L1 泛型"),
    ("L02_concurrency", "L2 高并发"),
    ("L03_async_preempt", "L3 异步抢占"),
    ("L04_scheduler_stack", "L4 协程调度/栈"),
    ("L05_alloc_gc", "L5 大量内存分配"),
    ("L06_reflect_iface", "L6 反射与接口"),
    ("L07_unsafe_zerocopy", "L7 unsafe 与零拷贝"),
    ("L08_asm_simd", "L8 汇编与 SIMD"),
    ("L09_cgo", "L9 cgo"),
    ("L10_codegen", "L10 代码生成"),
    ("L11_compiler_opts", "L11 编译期优化"),
    ("L12_init_link", "L12 初始化与链接"),
    ("L13_runtime_ds", "L13 运行时数据结构"),
    ("L14_debug_info", "L14 调试信息"),
    ("L15_build_tags", "L15 构建标签与多平台"),
    ("L16_template_interp", "L16 模板/解释器/元编程"),
    ("L17_stackmap", "L17 栈映射"),
    ("L18_gc_lifecycle_wb", "L18 GC 生命周期与写屏障"),
    ("L19_netpoll_timer_signal", "L19 netpoll/定时器/信号"),
]


def parse_frontmatter(path):
    with open(path, encoding="utf-8") as f:
        text = f.read()
    m = re.match(r"^---\n(.*?)\n---\n", text, re.S)
    meta = {}
    if not m:
        return meta
    key = None
    for line in m.group(1).splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        item = re.match(r"^\s*-\s*(.+)$", line)
        if item and key:
            meta.setdefault(key, [])
            if isinstance(meta[key], list):
                meta[key].append(item.group(1).strip())
            continue
        if ":" not in line:
            continue
        k, v = line.split(":", 1)
        key = k.strip()
        v = v.strip()
        if v.startswith("[") and v.endswith("]"):
            meta[key] = [x.strip() for x in v[1:-1].split(",") if x.strip()]
        elif v == "":
            meta[key] = []
        else:
            meta[key] = v
    return meta


def main():
    rows = {dim: [] for dim, _ in DIMS}
    missing_md = []
    for dim, _ in DIMS:
        d = os.path.join(CASES, dim)
        if not os.path.isdir(d):
            continue
        for name in sorted(os.listdir(d)):
            cdir = os.path.join(d, name)
            if not os.path.isfile(os.path.join(cdir, "main.go")):
                continue
            md = os.path.join(cdir, "case.md")
            if not os.path.isfile(md):
                missing_md.append(f"{dim}/{name}")
                meta = {}
            else:
                meta = parse_frontmatter(md)
            rows[dim].append({
                "name": name,
                "priority": meta.get("priority", ""),
                "tags": meta.get("tags", []),
                "requires": meta.get("requires", []),
                "sources": meta.get("sources", []),
            })

    lines = []
    lines.append("# 用例覆盖矩阵（自动生成）")
    lines.append("")
    lines.append("> 由 `tools/gen_matrix.py` 扫描 `cases/*/*/case.md` 生成；不要手改。")
    lines.append("")
    lines.append("## 维度汇总")
    lines.append("")
    lines.append("| 维度 | 名称 | 用例数 | P0 | P1 | P2 | 目标 | 状态 |")
    lines.append("|---|---|---:|---:|---:|---:|---:|---|")
    total = 0
    for dim, title in DIMS:
        cs = rows[dim]
        n = len(cs)
        total += n
        p0 = sum(1 for c in cs if c["priority"] == "P0")
        p1 = sum(1 for c in cs if c["priority"] == "P1")
        p2 = sum(1 for c in cs if c["priority"] == "P2")
        status = "✅" if n >= TARGET else ("🔶" if n else "⬜")
        lines.append(f"| {dim} | {title} | {n} | {p0} | {p1} | {p2} | {TARGET} | {status} |")
    lines.append(f"| **合计** | | **{total}** | | | | **{TARGET * len(DIMS)}** | |")
    lines.append("")

    for dim, title in DIMS:
        cs = rows[dim]
        if not cs:
            continue
        lines.append(f"## {dim} — {title}（{len(cs)}）")
        lines.append("")
        lines.append("| 用例 | 优先级 | requires | tags | 来源 |")
        lines.append("|---|---|---|---|---|")
        for c in cs:
            tags = ", ".join(c["tags"]) if isinstance(c["tags"], list) else str(c["tags"])
            req = ", ".join(c["requires"]) if isinstance(c["requires"], list) else str(c["requires"])
            src = len(c["sources"]) if isinstance(c["sources"], list) else (1 if c["sources"] else 0)
            lines.append(f"| {c['name']} | {c['priority']} | {req} | {tags} | {src} |")
        lines.append("")

    if missing_md:
        lines.append("## 缺少 case.md")
        lines.append("")
        for m in missing_md:
            lines.append(f"- {m}")
        lines.append("")

    with open(OUT, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print(f"wrote {OUT}: {total} cases")


if __name__ == "__main__":
    main()
