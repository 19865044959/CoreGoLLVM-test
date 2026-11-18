---
id: l16_ast_rewrite
dim: L16_template_interp
priority: P2
sources:
  - golangci-lint@v2.0.2: test/fix_test.go:10 RunFix 将诊断修复写回源码
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:128 诊断附带 SuggestedFix/TextEdit
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:280 token.NewFileSet + parser.ParseFile 驱动源码分析
  - sqlc@v1.28.0: internal/source/mutate_test.go:14 单处源码编辑（位置+旧串校验）
  - sqlc@v1.28.0: internal/source/mutate_test.go:89 多处编辑的排序与冲突处理
tags: [L16, goast, go-parser, go-format, ast-rewrite, go-types, GC, TORTURE_ROUNDS]
requires: [goast]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

解析一段 Go 源码后做 AST 级重写：批量重命名标识符（函数/变量/常量）、替换整型与字符串字面量、向 `File.Decls` 追加 `const Rewritten = true`，再用 `go/format.Node` 打印、重新解析并 `go/types` 类型检查，验证 gofmt 幂等与作用域符号。每轮更换常量值，打印后强制 GC 再格式化/类型检查，校验 AST 与位置表跨 GC 稳定。对应 golangci-lint fix 与 sqlc source mutate 的编辑管线。

# 期望行为

1. 输出含 `func modernCalc(a, b int) int`、`maxLimit = <200..260>`、`var modernName = "modern"`、`return total * 3`、`const Rewritten = true`；
2. 输出不再包含 `legacy` 与 `limit`；类型检查后 Scope 只有新名字，`maxLimit` 常量值为本轮替换值；
3. 原 AST 的函数名位置仍映射到第 5 行；重命名后节点仍可定位；
4. 对打印结果再次 `format.Node` 逐字节一致（gofmt 幂等）；
5. GC 后同一 AST 再次格式化输出不变，重新类型检查仍通过且常量值一致；
6. 最后一行 `ALL PASS l16_ast_rewrite`。

# 失败判读

- `formatted output missing`：AST 重写未生效或 `format.Node` 丢弃新增节点；
- `type check rewritten`：重写产生非法标识符/重复声明，或 go/types 处理新节点失败；
- `scope still has`：重命名遗漏（AST 遍历未覆盖所有 Ident）；
- `formatting not idempotent`：打印器对 NoPos 节点处理不稳定；
- `output changed after GC` / 崩溃：AST 节点或 token.FileSet 被破坏；
- 超时：`TORTURE_ROUNDS` 过大。

# 参数

- `TORTURE_ROUNDS`：默认 10–14 轮，可放大做长压测（上限 200000）；
- `UAF_SEED`：决定轮数与每轮常量替换值，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_ast_rewrite/*.go
TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=efence=1 /tmp/case
```
