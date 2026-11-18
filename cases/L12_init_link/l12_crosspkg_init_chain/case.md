---
id: l12_crosspkg_init_chain
dim: L12_init_link
priority: P1
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 一个 main 包 blank import 多个 testdata 子包，子包再各自依赖 base 子包
  - caddy@v2.10.0: modules_test.go:22 全局模块表在包 init 完成后才可查询
  - gitea@v1.23.5: models/renderhelper/main_test.go:14 TestMain 在子包初始化完成后注入回调
tags: [L12, module, cross-package, init-order, linear-chain, diamond-import, partial-order, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

module 同时包含一条线性导入链 `c -> b -> a` 和一个 diamond：`dl`、`dr` 都导入 `leaf`，`top` 再导入 `a`、`dl`、`dr`、`leaf`。每个包在包级变量初始化阶段与 `init` 中向共享 `sink` 写标记。用例只断言规范保证的偏序：依赖包先于导入者、包级变量先于同包 init、每包一次；`dl`/`dr` 的相对顺序未定义，故意不做断言。

# 期望行为

1. 全局 trace 15 项：7 个包各 2 项加 `main.init`；每个标记恰出现一次；
2. 线性链严格有序：`c.var < c.init < b.var < b.init < a.var < a.init`；
3. diamond 中 `leaf.init < dl.var`、`leaf.init < dr.var`，且 `a/dl/dr/leaf` 的 init 都早于 `top.var`；
4. 每包的 `.var` 与 `.init` 相邻（单包初始化期间不会被其他包插入）；
5. 根包 `mainSnapshot`（14 项）在 `main.init` 之前捕获，等于 trace 前缀；
6. 计算值 `a.V==22`、`dl.V==10`、`dr.V==15`、`leaf.V==5`、`top.V==250`；
7. 并发只读 + 显式 GC 后 trace 与值不变。

# 失败判读

- `c completes before b` 等失败：导入依赖方向被反转；
- `block pkg: var then init` 失败：同包变量/init 交错或 init 重放；
- `main var snapshot sees all imported packages` 失败：导入者变量早于依赖包 init；
- `dl`/`dr` 之间被错误断言导致的误报不在本用例范围：代码中没有该断言；
- GC 后 trace 变化：初始化期全局切片/字符串未被正确扫描。

# 参数

- `UAF_SEED`：并发 worker 起始偏移，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

```bash
cd cases/L12_init_link/l12_crosspkg_init_chain
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
