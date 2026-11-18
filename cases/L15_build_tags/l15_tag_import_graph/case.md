---
id: l15_tag_import_graph
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_import_graph/hub/route_linux.go:1 linux 分支导入 leaf/lnx
  - cases/L15_build_tags/l15_tag_import_graph/hub/route_other.go:1 非 linux 分支导入 leaf/oth
  - cases/L15_build_tags/l15_tag_import_graph/leaf/lnx/lnx.go:28 leaf 包 init 标记
  - cases/L15_build_tags/l15_tag_import_graph/hub/hub.go:7 变量初始化调用 tag 选择函数
  - cases/L15_build_tags/l15_tag_import_graph/main.go:104 init 标记约束校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, multi-package, import-graph, GOOS, init-order, cross-package-link, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

hub 包的两份互斥文件按 GOOS 选择不同的 import：linux 版导入 `leaf/lnx`，其他平台
版导入 `leaf/oth`。被导入的 leaf 包在 init 中登记标记并构造 `next` 指针；未选中的
leaf 包完全不进入 import 图。hub 的包级变量初始化器调用 tag 选择文件里的函数，是
"构建标签改变依赖图"的典型形态（quic-go/pion 的平台后端选择）。

## 期望行为

1. hub.Platform/Scale/Bias 与当前 GOOS 对应：linux 为 lnx/2/7，其他为 oth/5/9；
2. trace 序列恰好 5 个标记且各出现一次：trace、选中 leaf、route-<leaf>、hub、main；
3. 未选中 leaf 与 route 的标记计数均为 0；trace 最先，leaf 先于 hub，main 最后；
4. hub.NodeRef() 的接口视图返回选中 leaf 的 ID/Label/NextLabel；
5. GC 后接口视图与 payload 不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_import_graph`。

## 失败判读

- 构建报 `platformInfo` 重复/缺失：两份 route 文件的约束不是严格互补；
- 两个 leaf 标记同时出现：tag 选择的文件被同时编译或 leaf 被无条件导入；
- hub 值来自错误 leaf：import 图选择错误；
- main 早于 hub：跨包初始化依赖顺序错误。

## 参数

`BUILD_MODE=module`；无自定义 tag；期望值由 `runtime.GOOS` 推导。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_import_graph
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
