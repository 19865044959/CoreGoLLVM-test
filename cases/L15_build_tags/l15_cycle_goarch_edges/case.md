---
id: l15_cycle_goarch_edges
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_cycle_goarch_edges/a/link_amd64.go:5 amd64 后缀文件让 a 导入 b
  - cases/L15_build_tags/l15_cycle_goarch_edges/a/link_fallback.go:6 非 amd64 不导入 b
  - cases/L15_build_tags/l15_cycle_goarch_edges/b/link_amd64.go:4 amd64 下 b 不导入 a
  - cases/L15_build_tags/l15_cycle_goarch_edges/b/link_fallback.go:6 非 amd64 下 b 导入 a
  - cases/L15_build_tags/l15_cycle_goarch_edges/trace/trace.go:8 包 init 标记
  - cases/L15_build_tags/l15_cycle_goarch_edges/main.go:97 TORTURE_ROUNDS 校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOARCH, filename-suffix, import-cycle, init-order, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

构建标签可以改变 import 图，让字面互引的包集合在任何平台都不成环。amd64 下包 a
的 `link_amd64.go`（文件名后缀）导入 b，而 b 的 amd64 文件不导入 a；非 amd64 下
a 的 `!amd64` 文件不导入 b，b 的 `!amd64` 文件导入 a。两个平台都编译通过，且
依赖方向、包 init 顺序与运行期取值随 GOARCH 切换。

## 期望行为

1. amd64：`a.Edge()=="a->b"`、`b.Edge()=="b-standalone"`；
2. 非 amd64：`a.Edge()=="a-standalone"`、`b.Edge()=="b->a"`；
3. init 序列 amd64 为 `trace,b,a,main`，非 amd64 为 `trace,a,b,main`；
4. 4 个 init 标记各出现一次；
5. TORTURE_ROUNDS 轮 `a.Edge()+"|"+b.Edge()` 长度恒为平台固定值，总长等于 `R*len`；
6. 最终 GC 后依赖边、init 序列与 payload 不变；
7. 退出码 0，最后一行 `ALL PASS l15_cycle_goarch_edges`。

## 失败判读

- `import cycle not allowed`：后缀与 `!amd64` 文件同时参与编译，环未被切断；
- 某平台构建失败：互补文件约束写错；
- 依赖边方向相反或 init 顺序相反：后缀/tag 选择错误；
- 校验和不符：循环中字符串或对象被破坏，失败行带 `seed=/rounds=`。

## 参数

- `TORTURE_ROUNDS`：默认 20000，可放大；
- `UAF_SEED`：写入失败信息；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_cycle_goarch_edges
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=100000 /tmp/case
GOARCH=arm64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.arm64 .
```
