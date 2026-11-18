---
id: l16_ast_walk
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/compile_test.go:12 parser.ParseFile 后逐个 ast.GenDecl/FuncDecl/Block/Expr 分派
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 源码 ParseFile(ParseComments) → AST → 诊断位置
tags: [L16, goast, go-parser, ast-walk, node-count, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

用 `go/parser` 解析一段包含 const/var/type/interface/方法/if/for/range/switch/复合字面量的源码，用自定义 `ast.Visitor` 遍历并统计节点类型、总节点数、最大深度、父子位置包含关系；再 GC 后重复遍历，并用 `go/printer` 打印 AST 后重新解析比对统计结果。对应 yaegi 的 AST 编译分派与 golangci-lint 的源码→AST 诊断管线。

# 期望行为

1. AST 总节点数 187、最大遍历深度 10、注释组 1、顶层声明 8；
2. `FuncDecl=4`、`GenDecl=4`、`IfStmt=2`、`ForStmt=1`、`RangeStmt=1`、`SwitchStmt=1`、`CaseClause=3`、`ReturnStmt=6`、`CallExpr=6`、`CompositeLit=2`、`Ident=66`、`BasicLit=19` 等计数与预期一致；
3. 所有节点满足 `Pos<=End` 且落在父节点区间内；顶层声明顺序为 const/var/type/type/func×4，函数顺序为 `Sum/classify/build/main`，`Sum` 接收者为 `*pair`，位置为 20:1（函数名 20:16）；
4. GC 后重复 `ast.Walk` 计数不变，注释文本与 token 位置仍可读；
5. `printer.Fprint` 输出重新解析后节点统计与函数名保持一致；
6. 最后一行 `ALL PASS l16_ast_walk`。

# 失败判读

- `FAIL: total nodes/max walk depth/count ...`：AST 结构或遍历实现与 Go 1.24 语义不一致，优先检查解析器/遍历器；
- `FAIL: nodes outside parent range`：Node 位置/End 计算错误；
- GC 后计数变化或 panic：AST 节点堆对象被提前回收或指针被破坏；
- `FAIL: reparse printed AST`：AST 打印与重解析不一致。

# 参数

- 无随机路径，不读取 `UAF_SEED`；可用 `GOGC`/`GODEBUG` profile 放大 GC 压力。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_ast_walk/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
