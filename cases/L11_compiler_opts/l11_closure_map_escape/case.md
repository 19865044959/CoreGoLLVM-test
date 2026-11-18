---
id: l11_closure_map_escape
dim: L11_compiler_opts
priority: P1
sources:
  - echo@v4.13.4: context_test.go:508 TestContextReset 清空池化上下文 store map，Reset 后容量复用
  - goccy-json@v0.10.5: benchmarks/bench_test.go:501 BenchmarkCodeUnmarshalReuse 复用同一解码目标与 map/slice 容量
  - fasthttp@v1.59.0: args_test.go:108 池化 Args 的 Acquire/Release，10 goroutine 复用底层存储
tags: [escape-analysis, closure, map, loop-variable, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟请求上下文池、JSON 解码复用与参数池中“闭包捕获 + map 逃逸”的组合：`makeStore` 在堆上建 map 并由闭包写入；闭包捕获的变量在重新赋值后必须看到新值；`for` 循环内创建的闭包按 Go 1.22 语义各自捕获当轮变量；返回的 map 存放闭包；map 中保存指针值并串成链。随后用 `clear` 清空 map，验证链引用仍让节点存活。`UAF_SEED` 决定条目数 24..40，关键节点处分配垃圾并两次 `runtime.GC()`。

# 期望行为

1. 闭包捕获变量而非值：`p` 由节点 0 改为节点 1 后闭包返回 1；
2. 每轮循环闭包在 GC 后仍返回各自下标 0..n-1；
3. map 中指针值、字符串 tag 与 `next` 链在 GC 后完整，链长 n、id 和为 `n(n-1)/2`；
4. map 中的闭包（double/triple/inc）在 GC 后可调用且结果正确；
5. map 结构体值的 `b == a*a`，GC 后总和为平方和公式；
6. 立即调用的不逃逸闭包求和正确；
7. `clear(map)` 后 len=0，但链头引用的节点跨 GC 仍完整；
8. 最后一行输出 `ALL PASS l11_closure_map_escape`。

# 失败判读

- `closure captures variable`：闭包被错误按值捕获；
- `per-iteration loop closures`：循环变量捕获语义错误（共享了同一变量）；
- `map entries/chain after GC`：map 或节点未被正确保留，或 GC 后内容被破坏；
- `map of closures`：函数值装箱/调用约定错误；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定 map 条目数 24..40，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_closure_map_escape/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_closure_map_escape/*.go
UAF_SEED=1 /tmp/case_n
```
