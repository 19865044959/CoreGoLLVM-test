---
id: l15_init_tag_edge
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_init_tag_edge/mid/edge_linux.go:1 linux 依赖边导入 za
  - cases/L15_build_tags/l15_init_tag_edge/mid/edge_other.go:1 非 linux 依赖边导入 zb
  - cases/L15_build_tags/l15_init_tag_edge/mid/mid.go:11 mid 无条件依赖 base
  - cases/L15_build_tags/l15_init_tag_edge/top/top.go:8 top 依赖 mid
  - cases/L15_build_tags/l15_init_tag_edge/main.go:106 包间 init 偏序断言
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, init-order, GOOS, multi-package, dependency-edge, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

模拟 caddy/traefik 的插件式依赖：mid 无条件依赖 base，同时按 GOOS 从 za/zb 中
选一条依赖边补上平台实现。构建标签改变的是包间依赖图，被选中包必须先于依赖者
完成 init。用例只断言 Go 规范保证的偏序（base 与 edge 彼此顺序不定），并用 trace
验证未选中包完全不参与初始化。

## 期望行为

1. linux 下 mid.Edge==11、top.Sum==16；其他平台 mid.Edge==13、top.Sum==18；
2. 七个 init 标记各出现一次：trace、base、选中 edge 包、edge-<pkg>、mid、top、main；
3. trace 最先；base、edge 包先于 mid；edge 包先于其 init 标记；top 先于 main；
4. 未选中 edge 包与其 init 标记计数为 0；
5. edge 包的 Node 接口视图在 GC 后仍返回选中包的 ID/Label；
6. 退出码 0，最后一行 `ALL PASS l15_init_tag_edge`。

## 失败判读

- 两个 edge 包标记同时出现：tag 选择文件同时编译/导入；
- mid 早于 edge：依赖图初始化顺序错误；
- 构建报重复符号：两份 edge 文件约束不互补；
- 值/标签来自错误包：GOOS 标签选择错误。

## 参数

`BUILD_MODE=module`；无自定义 tag；期望值由 `runtime.GOOS` 推导，`UAF_SEED` 不参与路径。

## 运行

```bash
cd cases/L15_build_tags/l15_init_tag_edge
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
