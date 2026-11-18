---
id: l16_types_scope_lookup
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:935 多次 Eval 共享全局作用域、局部作用域链与重声明规则
  - yaegi@v0.16.1: extract/extract_test.go:28 用 go/types 枚举包符号与类型
  - golangci-lint@v2.0.2: pkg/golinters/unconvert/testdata/unconvert.go:30 用 types.Info.Types 查询表达式类型
tags: [L16, goast, go-types, scopes, defs-uses, implicits, selections, shadowing, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

对一段含包级常量、结构体、方法、函数、块、for、if 与 type switch 的源码做 `go/types.Config.Check`，从 `types.Info` 驱动作用域与对象解析：包/文件/函数/块作用域链、`Defs`/`Uses`、shadowing、`Implicits[CaseClause]` 的 type-switch 隐式变量、`Selections` 的字段/方法选择。对应 yaegi 的全局/局部作用域链与 golangci-lint 基于 `types.Info` 的查询。

# 期望行为

1. 包作用域名字排序为 `K,Pair,f,use`；`K` 值精确为 3，`Pair` 有 A/B 两个字段，`f` 签名为 1 参 1 果且参数名 `n`；universe 含 `int`/`any`/`len`；
2. 函数作用域名字为 `n,x`，父作用域链为 file scope → package scope；
3. 块内 `x := x + K` 与外层 `x` 是两个对象、父作用域不同，内层 RHS 的 `x` 使用解析到外层对象；从内层块 `LookupParent("K", ...)` 命中包作用域；
4. for 变量 `i` 在 for scope、if 变量 `v` 在 if scope，均不泄漏到函数作用域；空白标识符 `_` 不产生对象；
5. type-switch 恰有一个 `*ast.CaseClause` 隐式对象 `t`（类型 int），guard 中 `t` 无 Defs，case 体内使用解析到该隐式对象；
6. `p.A`/`p.B` 为 `FieldVal`、索引 0/1；`p.Sum()` 为 `MethodVal`，接收者 `*demo.Pair`，表达式 `p.A` 类型为 int；
7. 三轮 GC 后包对象、函数对象、外层 `x`、`Selection.Obj()` 与方法集指针身份不变；
8. 末行输出 `ALL PASS l16_types_scope_lookup`。

# 失败判读

- `FAIL: package scope names`：包/文件作用域符号收集异常；
- `FAIL: shadowed x ...`：`Defs`/`Uses` 或 `LookupParent` 位置语义错误；
- `FAIL: implicits`：type-switch 隐式变量 untyped/错误归属；
- `FAIL: selection kind/recv`：方法集或选择器解析错误；
- GC 轮次失败：类型对象/作用域在 GC 后失效。

# 参数

- `UAF_SEED`：决定 GC 轮次抽查的包符号顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_scope_lookup/*.go
UAF_SEED=2 GODEBUG=clobberfree=1 /tmp/case
```
