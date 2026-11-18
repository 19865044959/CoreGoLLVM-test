---
id: l11_panic_deadcode
dim: L11_compiler_opts
priority: P0
sources:
  - klauspost-compress@v1.17.11: gzhttp/compress_test.go:353 TestMustNewGzipLevelHandlerWillPanic 通过 defer recover 校验 panic
  - goccy-json@v0.10.5: encode_test.go:483 TestDebugMode defer + recover 包裹可能 panic 的 MarshalJSON
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign.go:10 赋值后未被读取的 dead store
tags: [dce, dead-branch, unreachable-code, panic-recover, divide-by-zero, side-effect, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟序列化/压缩错误路径中"不该触发的 panic"：常量假分支、`switch` 未命中分支、零次循环、`const debug=false` 分支、`if false && f()` 短路分支、未被调用的闭包、`return` 之后的不可达语句、`if true { return }` 之后的 panic，以及死分支里的除零。同时构造运行期可达的 panic：显式字符串、自定义错误指针、循环中途 panic、越界、除零、`panic(nil)`、嵌套 recover 包装、GC 后 panic。副作用计数器与 recover 结果保留到 GC 后复核。

# 期望行为

1. 死分支全部不执行，`deadBranches` 命中数恒为 2；
2. 死分支中的 `runtimeZero` 不被调用、短路分支的 `sideBool` 不执行，副作用计数为 1；
3. `return` 后与常量 `if` 后的 panic 均不触发；
4. 可达分支的显式 panic、自定义 panic 值、循环 panic、嵌套包装文本均被正确 recover；
5. 运行期除零 panic 文本含 `integer divide by zero`，且副作用计数恰好加 1；
6. 运行期越界 panic 文本含 `index out of range`；不命中的索引路径返回 `no-panic`；
7. `panic(nil)` 被转换为非 nil 的 `PanicNilError`，文本含 `panic called with nil argument`；
8. GC 后 panic 仍可 recover，recover 字符串跨 GC 不变；
9. `if flag { panic(...) }` 的 true/false 两条路径分别 panic 与正常返回；
10. 最后一行输出 `ALL PASS l11_panic_deadcode`。

# 失败判读

- `dead branch ...` / `unreachable code ...`：死代码消除遗漏，执行了本不应执行的 panic 或副作用；
- `live panic did not fire` / `live bounds panic missing` / `live divide-by-zero missing`：可达 panic 被错误删除或移动；
- `panic(nil) semantics`：recover 对 nil panic 的包装语义错误；
- `side effect counters ...`：DCE 越过可观察全局写；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定循环次数 `8..15` 与中途 panic 位置，失败输出带种子；
- 本用例为 P0，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_panic_deadcode/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_panic_deadcode/*.go
UAF_SEED=1 /tmp/case_n
```
