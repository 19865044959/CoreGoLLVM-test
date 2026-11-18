---
id: l11_inline_budget
dim: L11_compiler_opts
priority: P1
sources:
  - goccy-json@v0.10.5: encode_test.go:2272 TestRecursivePtrHead 递归指针结构的 JSON 编码
  - goccy-json@v0.10.5: decode_test.go:2760 TestUnmarshalRecursivePointer 自引用接口触发的递归解码
  - klauspost-compress@v1.17.11: gzip/gzip_test.go:581 BenchmarkCompressAllocations 每轮新建 Writer 的内联/逃逸敏感路径
tags: [inline, recursion, inline-budget, noinline, argument-evaluation, stack-growth, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 JSON 递归编码/解码与压缩 Writer 热路径中的内联边界：递归函数自身不会被内联，但递归体内的极小型叶子函数、线性调用链中的包装函数是内联候选。用例在递归求和中调用可内联叶子（并统计调用次数），同时提供 `//go:noinline` 对照；用带副作用的参数构造递归调用以检查求值顺序；构建 8..10 层完全二叉树，在显式 GC 后复核递归遍历；再以 2500..2999 层线性递归触发多次栈增长。

# 期望行为

1. 可内联叶子参与的递归和等于 `3*n*(n+1)/2+n`，叶子每层恰好调用一次；
2. `//go:noinline` 版本的递归和与调用次数同样正确；
3. 递归调用中一对副作用参数的求值顺序为每层先 1 后 2，总次数 2n；
4. 5 层包装调用链与全 `//go:noinline` 链在正负输入下结果相同，且等于展开公式；
5. 大函数体版本与 noinline 版本结果一致；
6. 二叉树递归求和等于 `3*sum(1..nodeCount)+nodeCount`，节点数与左脊柱值正确；
7. GC 后树和、节点数、根与左脊柱字段不变；
8. 深层递归在 GC 前后均得到同一公式值；
9. 最后一行输出 `ALL PASS l11_inline_budget`。

# 失败判读

- `recursive sum with inlinable leaf` / `noinline recursive sum`：递归边界的返回值被内联改写或栈增长后参数丢失；
- `inlinable leaf called once per level`：叶子副作用被重复执行或消除；
- `argument evaluation order`：内联后参数求值顺序改变；
- `inline call chain` / `large-body`：包装链或大函数在两种构建下语义不一致；
- `tree ... after GC`：树节点被提前回收或字段损坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定线性递归深度 2500..2999、树深 8..10，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_inline_budget/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_inline_budget/*.go
UAF_SEED=1 /tmp/case_n
```
