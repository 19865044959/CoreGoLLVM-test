---
id: l15_var_init_dep_files
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_var_init_dep_files/a_vars.go:1 文件内声明顺序与依赖
  - cases/L15_build_tags/l15_var_init_dep_files/b_linux_vars.go:1 tag 文件变量依赖后置文件
  - cases/L15_build_tags/l15_var_init_dep_files/z_vars.go:1 依赖先于声明初始化
  - cases/L15_build_tags/l15_var_init_dep_files/main.go:79 期望初始化序列
  - cases/L15_build_tags/l15_var_init_dep_files/main.go:97 TORTURE_ROUNDS 遍历
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, package-variable, init-order, dependency-order, build-tag, GOOS, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

同一包内跨文件包级变量的初始化按依赖关系拓扑排序，而不是按文件或声明顺序。
`b_linux_vars.go` 文件名排在 `m_vars.go` 前，但 `Tagged` 依赖 `z_vars.go` 的
`Fourth`，必须最后初始化；`z_vars.go` 中 `Fourth` 声明在 `Fifth` 后，也必须
先算 `Fifth`。tag 二选一文件同时改变最终值与初始化序列末项。

## 期望行为

1. 初始化序列 linux 为 `beta,first,second,alpha,third,fifth,fourth,tagged`；
2. 非 linux 末项为 `tagged-other`，其余相同；
3. 值链：2,10,15,3,20,25,30；linux 下 `Tagged=35`，其他为 37；
4. `TaggedTail=Tagged+1`、`mainTotal=Tagged+1`；
5. main.go 中的 `head` 链表按 `Tagged→Fourth→Fifth` 构造；
6. TORTURE_ROUNDS 轮遍历链表的 ID 校验和等于按 3 周期闭式值；
7. 最终 GC 后值、初始化序列、链表不变；
8. 退出码 0，最后一行 `ALL PASS l15_var_init_dep_files`。

## 失败判读

- 序列末项在 `m` 之前：跨文件依赖排序错误；
- `fourth` 在 `fifth` 前：同文件声明顺序未让位于依赖；
- 值链整体偏移：初始化器执行顺序错误；
- tag 分支值错误：GOOS 文件选择错误；
- 失败信息带 `seed=/rounds=`：用同值参数复现。

## 参数

- `TORTURE_ROUNDS`：默认 5000，可放大；
- `UAF_SEED`：决定每轮遍历起点 (r+seed)%3，失败时打印；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_var_init_dep_files
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
```
