---
id: l15_var_func_dep_files
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_var_func_dep_files/a_use.go:4 viaFunc 函数体引用 A
  - cases/L15_build_tags/l15_var_func_dep_files/m_chain.go:4 chainC 函数体引用 B
  - cases/L15_build_tags/l15_var_func_dep_files/b_linux_vars.go:8 viaTagged 函数体引用 C
  - cases/L15_build_tags/l15_var_func_dep_files/b_other_vars.go:8 非 linux tag 分支
  - cases/L15_build_tags/l15_var_func_dep_files/z_base.go:4 A 声明在字典序最后
  - cases/L15_build_tags/l15_var_func_dep_files/main.go:34 buildHead 函数体引用全部变量
  - cases/L15_build_tags/l15_var_func_dep_files/main.go:103 TORTURE_ROUNDS 链遍历
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, package-variable, init-order, dependency-order, build-tag, GOOS, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

包级变量的依赖分析沿函数体传递：`B` 通过 `viaFunc` 的函数体依赖 `A`，`C` 通过
`chainC` 依赖 `B`，tag 文件的 `Tagged` 通过 `viaTagged` 依赖 `C`，main 的 `head`
通过 `buildHead` 依赖上述全部变量。实际声明/文件顺序为 B、Tagged、C、A，但初始化
必须按函数依赖拓扑排序为 A→B→C→Tagged→head。TORTURE_ROUNDS 轮遍历 head 链。

## 期望行为

1. 初始化 trace 为 `A,B,C,tagged-linux,head,init-tag`；非 linux 为 `tagged-other`；
2. 值链 `A=5`、`B=6`、`C=13`、`Tagged=16`（linux）/17（其他）；
3. 链表为 `Tagged→C→B→A`，四项且以 nil 结束；
4. tag 文件的 init 在全部变量就绪后执行（`initSawVars==true`）；
5. TORTURE_ROUNDS 轮按 `(r+seed)%4` 起点遍历链表，校验和等于闭式值；
6. 最终 GC 后 trace、值链与链表不变；
7. 退出码 0，最后一行 `ALL PASS l15_var_func_dep_files`。

## 失败判读

- trace 按声明顺序（B,Tagged,C,A）出现：函数体依赖未被纳入拓扑排序；
- `Tagged` 在 `C` 前：跨文件 tag 变量依赖错误；
- 值链整体偏移：初始化器求值顺序错误；
- 链表在 GC 后断链/字段损坏：全局指针写屏障或栈映射问题；
- 失败行带 `seed=/rounds=`：用同值参数复现。

## 参数

- `TORTURE_ROUNDS`：默认 20000，可放大；
- `UAF_SEED`：决定每轮遍历起点，失败时打印；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_var_func_dep_files
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
```
