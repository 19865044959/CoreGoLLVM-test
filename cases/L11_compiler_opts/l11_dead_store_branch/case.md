---
id: l11_dead_store_branch
dim: L11_compiler_opts
priority: P0
sources:
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign.go:10 循环内赋值后立即被覆盖的 ineffectual assignment
  - golangci-lint@v2.0.2: pkg/golinters/staticcheck/testdata/staticcheck.go:11 自赋值 x = x
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign_cgo.go:33 同类循环覆盖赋值
tags: [dead-store-elimination, dce, branch, switch, phi, loop, aliasing, side-effect, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 linter 自身检测的无效赋值在 SSA 分支合并点上的处理：if 两臂与 switch 各分支写入的局部变量随后被覆盖；仅一条路径会覆盖的存储必须保留；循环 phi 中每轮被覆盖的中间值、结构体整体覆盖、数组两轮写入、别名指针可见性以及死存储 RHS 的副作用/panic 都不能被错误消除。

# 期望行为

1. if 两臂都被覆盖时最终值为 40，RHS 副作用顺序为 1、2/3、4；
2. 仅 true 路径覆盖时返回 11 且副作用为 5；false 路径保留 7 且无副作用；
3. switch 各分支的死存储 RHS 副作用恰好执行一次，最终值均为 50；
4. 循环 phi 的最终值等于 `2*(n-1)`，累加和等于 `n*(n-1)`；
5. 循环内连续覆盖的 `last` 最终等于 `n+9`；
6. 结构体整体覆盖后字段值为 `(3,"y")`；分支后统一覆盖时别名读到最终值 4；
7. 数组两轮写入最终为 `[0,1,4,9]`；死存储 RHS 中的 panic 仍被 recover 捕获；
8. 显式 GC 后上述值与副作用轨迹不变；
9. 默认构建与 `-gcflags='all=-N -l'` 构建全部断言一致；
10. 最后一行输出 `ALL PASS l11_dead_store_branch`。

# 失败判读

- `killed branch side effects`：死存储 RHS 的副作用被误删或重复执行；
- `one-path dead store` / `live store preserved`：部分路径覆盖被错误当成全路径死存储；
- `loop phi accumulated dead stores`：循环携带的存储被错误消除；
- `branch alias final store`：别名可见的最终值被错误回退；
- `dead store RHS panic preserved`：RHS 中的 panic 被当作纯操作删除；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定循环次数 32..63，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_dead_store_branch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_dead_store_branch/*.go
UAF_SEED=1 /tmp/case_n
```
