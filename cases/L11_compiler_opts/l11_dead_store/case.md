---
id: l11_dead_store
dim: L11_compiler_opts
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign_cgo.go:33 循环内赋值后立即被覆盖的 dead store
  - golangci-lint@v2.0.2: pkg/golinters/staticcheck/testdata/staticcheck.go:17 自赋值 x = x 的 dead store
tags: [dead-store-elimination, dce, aliasing, side-effect, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 lint 规则与 ORM 更新链中“赋值后未被读取”的 store：本地变量连续覆盖、结构体字段覆盖、条件分支覆盖、循环内除最后一次外全被覆盖；同时构造指针别名使 store 可观察（`p := &s.a; s.a = 2; *p`）、数组/切片元素、map 重复写入、全局变量覆盖、接口指针读写。被覆盖的 store 右侧若含函数调用或 panic，副作用必须保留。指针字段 store 必须让对象跨 GC 存活，最终指针值必须是最后一次写入。条目数由 `UAF_SEED` 决定。

# 期望行为

1. 被覆盖的本地/字段/条件/循环 store 最终值正确；
2. 右侧 `dsSideEffect` 调用仍执行（副作用计数精确增加 7、5、6）；
3. 通过指针别名读取的字段/数组元素为最后一次写入值；
4. map 重复写入最终为 9，且两次右侧副作用都执行（累计 +11）；
5. 右侧 panic 的 store 仍 panic，并输出 `caught:store-panic`；
6. 指针字段在 GC 后保持 41/2/5/7 等最终值，旧节点与新节点均不被破坏；
7. 全局覆盖最终为 2，GC 后不变；循环 store/循环携带 store 与公式一致；
8. 最后一行输出 `ALL PASS l11_dead_store`。

# 失败判读

- `dead store RHS side effect executed`：DCE 错误删除带副作用的右侧；
- `field/array store visible through alias`：别名可见的 store 被错误删除；
- `map overwrite and RHS side effects`：map store 或右侧调用被删除；
- `store RHS panic preserved`：panic 控制流被 DCE 破坏；
- `pointer field keeps object alive` / `nil overwrite`：指针字段 store 丢失或被 GC 提前回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定循环条目数 32..63，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_dead_store/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_dead_store/*.go
UAF_SEED=1 /tmp/case_n
```
