---
id: l11_inline_budget_variadic
dim: L11_compiler_opts
priority: P1
sources:
  - goccy-json@v0.10.5: encode_test.go:2272 TestRecursivePtrHead 递归指针结构编码
  - fasthttp@v1.59.0: stackless/func_test.go:10 TestNewFuncSimple any 参数 + 类型断言调用 4096 次
  - gorm@v1.30.0: clause/values_test.go:21 批量 INSERT 的 [][]interface{} 包装
tags: [inline, inline-budget, variadic, multi-return, noinline, slice-aliasing, escape-analysis, argument-evaluation, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 JSON 递归编码、无栈函数包装与 ORM 批量参数展开中的内联边界：小型包装函数链逐层转发到可内联叶子，另有多返回值包装、变参求和/转发/就地修改、`append` 到变参切片、带副作用的参数求值顺序，以及被内联预算排除的大函数体。每个热路径都提供 `//go:noinline` 对照；变参切片和指针元素逃逸到全局后做显式 GC 再复核。

# 期望行为

1. 5 层包装链与全 `//go:noinline` 链结果相同，且等于 `9*x+5`；每层叶子副作用恰好 2 次；
2. 多返回值包装的值等于 `(3*x+1)*1000+(3*x+4)`，两种构建下叶子调用次数一致；
3. 变参求和/转发结果正确，`xs...` 透传时对 `xs[0]` 的修改对调用方可见（引用同一底层数组）；
4. 变参内 `append` 在容量内不改变调用方可见前缀，容量不足时扩容并与调用方分离；
5. 三实参求值顺序固定为 1、2、3，两种构建一致；
6. 大函数体版本与 noinline 版本逐输入一致，且只调用一次；
7. 变参切片 `[]*ivNode` 与元素跨显式 GC 保持指针、值和标签完整；
8. 默认构建与 `-gcflags='all=-N -l'` 构建全部断言一致；
9. 最后一行输出 `ALL PASS l11_inline_budget_variadic`。

# 失败判读

- `inline/noinline wrapper chain` / `multi-return wrapper`：内联改写返回路径或重复执行叶子；
- `variadic slice aliases caller storage`：变参转发被错误复制成新底层数组；
- `variadic extend leaves caller prefix`：append 内联后覆盖调用方可见元素；
- `argument evaluation order`：内联后参数求值顺序改变；
- `pointer variadic ... after GC`：仅由变参切片保活的指针被回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定变参长度 24..36 与指针元素数 6..10，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_inline_budget_variadic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_inline_budget_variadic/*.go
UAF_SEED=1 /tmp/case_n
```
