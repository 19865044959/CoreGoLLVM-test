---
id: l15_init_phase_order
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_init_phase_order/a_vars.go:4 变量阶段与同文件两个 init
  - cases/L15_build_tags/l15_init_phase_order/b_linux_setup.go:6 tag 文件的变量与 init
  - cases/L15_build_tags/l15_init_phase_order/b_other_setup.go:6 非 linux 互斥槽位
  - cases/L15_build_tags/l15_init_phase_order/m_vars.go:3 依赖 tag 变量的后续变量
  - cases/L15_build_tags/l15_init_phase_order/z_late.go:5 最后一个 init 复核变量阶段已完成
  - cases/L15_build_tags/l15_init_phase_order/main.go:66 期望 init 序列
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, init-order, package-variable, file-name-order, GOOS, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

包初始化分为两个阶段：先执行全部包级变量初始化器，再按文件名字典序执行各文件的
`init`。`b_linux_setup.go` / `b_other_setup.go` 由 GOOS 二选一，在变量阶段和 init
阶段都占用 b 槽位；`z_late.go` 的 init 在最后复核所有变量已就绪。main 断言两个阶段
的边界不会被 tag 文件打乱。

## 期望行为

1. linux 的 trace 恰为 `var-a,var-tag-linux,var-m,init-a1,init-a2,init-tag-linux,init-z`；
2. 非 linux 为 `var-a,var-tag-other,var-m,init-a1,init-a2,init-tag-other,init-z`；
3. `VA==1`，`VTag` 为 10（linux）/20（其他），`VM==VTag+1`；
4. 所有 `var-*` 记录都早于任何 `init-*` 记录；
5. `z_late.go` 的 init 看到 `payload != nil && payload.v == VM`；
6. 两次 GC 后 trace、变量值与 payload 不变；
7. 退出码 0，最后一行 `ALL PASS l15_init_phase_order`。

## 失败判读

- `init-*` 早于某个 `var-*`：变量阶段与 init 阶段被混排；
- tag 槽位缺失或出现两个 tag 记录：GOOS 文件选择错误；
- `init-a2` 在 `init-a1` 前：同文件多个 init 未按源码顺序；
- `initSawAllValues` 为假：init 提前执行或 payload 未构建；
- GC 后值变化：变量对象被破坏。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_init_phase_order
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
```
