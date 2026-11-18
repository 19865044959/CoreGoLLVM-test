---
id: l16_ast_comment_rewrite
dim: L16_template_interp
priority: P1
sources:
  - golangci-lint@v2.0.2: test/fix_test.go:10 RunFix 将诊断修复写回源码
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 ParseFile(ParseComments) 驱动源码分析
  - sqlc@v1.28.0: internal/source/mutate_test.go:14 单处源码编辑（位置+旧串校验）
  - sqlc@v1.28.0: internal/source/mutate_test.go:89 多处编辑的排序与冲突处理
  - sqlc@v1.28.0: internal/endtoend/fmt_test.go:16 格式化幂等性
tags: [L16, goast, ast-rewrite, comment-map, go-format, go-types, idempotent, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

解析带包注释、函数 doc、行内/尾随注释的源码，用 `ast.NewCommentMap` 关联注释后做四类重写：函数/调用重命名、`+` 改 `*`、整型字面量改值、删除无用 `var` 声明；同时替换 doc 注释中的旧函数名。重写后 `cmap.Filter` 丢弃被删节点的注释并保留其余注释，`format.Node` 打印，再用 `go/types` 复检、gofmt 幂等复检，GC 后重新打印与重映射。对应 golangci-lint 的 SuggestedFix 写回与 sqlc 的源码编辑场景。

# 期望行为

1. 输出包含 `func fastAdd`、`return a * b // trailing add note`、`return fastAdd(1, <lit>) // call note`，`lit` 由轮次决定；
2. 包注释、函数 doc、行内注释、尾随注释、`run` doc、调用注释全部保留，doc 中的旧名已更新为 `fastAdd`；
3. 被删 `unused` 声明的两条注释（`unused helper...`、`remove this note`）消失，注释组数从 8 降为 6；
4. 格式化结果可重新解析且再次 gofmt 逐字节一致；
5. `go/types` 检查无错误，作用域含 `fastAdd/run`、不含 `legacyAdd/unused`，`fastAdd` 类型为 `func(a int, b int) int`；
6. GC 后 AST 节点、运算符改写、位置映射、6 个注释组与打印结果均不变，`CommentMap.Filter` 仍得到 6 组；
7. 最后一行 `ALL PASS l16_ast_comment_rewrite`。

# 失败判读

- `output missing`：重写未生效或注释被丢弃；
- `comment groups=N want 6`：`CommentMap.Filter` 错误保留/丢弃注释；
- `formatting not idempotent`：`format.Node` 与 FileSet 位置不一致；
- `type check` 非空：重写产生非法引用或删除了仍被使用的声明；
- GC 后节点/注释/输出变化：AST、CommentMap 或字符串被破坏。

# 参数

- `UAF_SEED`：决定轮数（4–6）与替换字面量，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_ast_comment_rewrite/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 GODEBUG=clobberfree=1 /tmp/case
```
