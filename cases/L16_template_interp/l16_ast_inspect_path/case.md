---
id: l16_ast_inspect_path
dim: L16_template_interp
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:16 源码 AST → 诊断 Pos/SuggestedFix，需定位其所属函数/声明
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 parser.ParseFile(ParseComments) 驱动遍历
  - sqlc@v1.28.0: internal/source/mutate_test.go:14 按位置+旧串做单处源码编辑
  - yaegi@v0.16.1: interp/compile_test.go:12 逐个 ast.GenDecl/FuncDecl/Block/Expr 分派
tags: [L16, goast, ast-inspect, path-stack, shadowing, enclosing-func, pruning, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

实现 linter 风格的路径栈遍历：用 `ast.Inspect` 维护节点栈，为每个 `*ast.Ident` 记录定义/使用、行号、所在路径和最近的外层 `FuncDecl`；用 `ast.Object` 判定定义并识别 shadowing。另做一次在 `FuncLit` 处剪枝的遍历，验证函数字面量内部标识符计数差值。对应 golangci-lint 将诊断映射到语句/函数与 sqlc 位置编辑前的 AST 定位。

# 期望行为

1. 顶层函数顺序为 `Score`、`walk`，节点数 > 60、最大深度 >= 8；
2. 全量遍历与剪枝 FuncLit 的标识符数量差恰为 5（字面量内的 `v,int,int,v,Limit`）；
3. `total` 出现 5 次、仅 1 次定义（第 19 行），所有出现共享同一 `ast.Object`，所属顶层函数均为 `walk`；
4. `v` 出现 6 次，range 变量（第 23 行）与函数字面量参数（第 20 行）是两个不同对象，其余使用分别解析到两者之一；
5. `Limit` 出现 3 次：包级定义（第 4 行）、`Score` 内使用（第 12 行）、函数字面量内使用（第 21 行，路径含 `FuncLit`、`ReturnStmt`），三者指向同一对象；
6. `r`/`n`/`f` 各有一个定义、同名字符串内对象一致；`f` 的调用路径含 `CallExpr` 与 `AssignStmt`；
7. 三轮 GC 后重建索引，标识符序列、行号、所属函数与 `total` 对象指针不变；
8. 末行输出 `ALL PASS l16_ast_inspect_path`。

# 失败判读

- `FAIL: pruned ident diff`：`ast.Inspect` 剪枝返回值的栈平衡处理错误；
- `FAIL: shadowed v objects must differ`：`ast.Object` 解析或作用域识别错误；
- `FAIL: ... topFunc`：路径栈中 `FuncDecl` 查找逻辑错误；
- GC 轮次失败：AST/对象未被正确保留；
- panic：栈 pop 与 nil 回调不匹配。

# 参数

- `UAF_SEED`：决定 GC 轮次抽查标识符的顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_ast_inspect_path/*.go
UAF_SEED=3 GODEBUG=clobberfree=1 /tmp/case
```
