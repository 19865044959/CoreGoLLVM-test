---
id: l16_types_check
dim: L16_template_interp
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/unconvert/testdata/unconvert.go:30 用 types.Info.Types 比较转换前后类型恒等
  - yaegi@v0.16.1: extract/extract_test.go:28 用 go/types 枚举包符号与类型
tags: [L16, goast, go-types, type-check, diagnostics, method-set, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

用 `go/types.Config.Check` 对一段自包含合法源码做类型检查（含 struct、值/指针接收者方法、接口、接口实现断言、常量表达式、返回 `(*T, error)` 的函数），再从 `types.Info`/`Scope`/`MethodSet` 查询结果；随后检查 8 段非法源码，验证错误文本与行号可判读；最后 GC 并重新查询所有类型对象。对应 golangci-lint 基于 `types.Info` 的规则与 yaegi 的 `go/types` 符号导出。

# 期望行为

1. 合法源码零错误；`Base` 常量折叠为 `12`（行 15）；`Twice` 类型串为 `func(n int) int`，`SafeDiv` 为 `func(a int, b int) (int, error)`；
2. `Point` 与 `*Point` 方法集均含 `Sum`，`Point` 实现 `Sumer`；`*MyErr` 实现 `error` 而 `MyErr` 不实现；
3. `types.Info.Types` 中 `3*4` 的常量值为 `12`；
4. 非法源码分别得到含 `undefined: missing`、`cannot use ... as string`、`missing return`、`x redeclared in this block`、`cannot use "s" ... as int`、`invalid operation: 1 + true`、`undefined: Missing`、`not enough return values` 的错误，且均带 `文件名:行:列` 前缀与预期行号；
5. GC 后 Scope/常量值/方法集/`types.Info`/`ast.File` 仍可用；
6. 最后一行 `ALL PASS l16_types_check`。

# 失败判读

- `FAIL: legal source errors`：合法源码被判错，检查类型检查器对方法/接口/常量的处理；
- `FAIL: const Base value/line`：常量折叠或位置表错误；
- `FAIL: ... no error contains`：非法源码未被拒绝或错误文本与 Go 1.24 不一致；
- `FAIL: ... error lines=...`：错误位置映射错误；
- GC 后查询失败/崩溃：`types.Package`/`types.Info` 对象图指针被破坏。

# 参数

- `UAF_SEED`：打乱非法用例检查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_check/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
