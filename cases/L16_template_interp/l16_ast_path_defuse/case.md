---
id: l16_ast_path_defuse
dim: L16_template_interp
priority: P1
sources:
  - ent@v0.14.4: entc/integration/config/config_test.go:58 ParseComments 后 ast.Inspect 定位字段
  - ent@v0.14.4: entc/integration/config/config_test.go:60 Inspect 回调中的节点类型断言
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 单文件解析驱动分析
  - tailscale@v1.80.2: cmd/viewer/viewer_test.go:41 parser + types.Info 联合遍历
tags: [L16, goast, ast-inspect, path-stack, defs-uses, scopes, selections, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

模拟 golangci-lint / ent 代码生成器这类分析器：一边用 `ast.Inspect` 维护节点路径栈，
一边用 `go/types.Info` 的 `Defs/Uses/Implicits/Selections` 解析标识符对象。既要知道某个
标识符位于哪个函数、哪个函数字面量、哪条语句路径，也要确认它绑定到哪个包级/局部对象。
路径栈还用于剪枝：跳过函数字面量子树后复算索引，检查关键标识符是否被正确排除。

## 期望行为

1. 解析与类型检查成功，路径索引节点 >80、最大深度 ≥8、标识符事实 ≥30；
2. 每个保留的标识符要么是定义要么是使用，且 `types.Object` 非空；
3. `sum` 1 定义 4 使用同对象且属于 `total`；`x` 为 range 定义 + 函数字面量内 2 处使用；
4. `x` 使用路径含 `FuncLit/IfStmt/ReturnStmt/BinaryExpr`，`y` 使用/定义均在函数字面量内；
5. `f` 定义与调用同对象，且路径不含 `FuncLit`；
6. `t.N` 的路径含 `SelectorExpr`，`Selections` 为 `FieldVal`，`N` 解析到结构体字段对象；
7. type-switch 守卫 `v` 既无 `Defs` 也无 `Uses`，case 内 `v` 指向 `Implicits[CaseClause]`；
8. 剪枝函数字面量后事实数恰少 7 条，`y` 全部消失、`f` 保留；
9. 3 轮 GC 后完整路径摘要与目标对象身份不变（按 `UAF_SEED` 打乱抽查顺序）。

## 失败判读

- `FAIL: ... path=...`：AST 路径栈压栈/弹栈错误，或函数字面量剪枝后未恢复栈；
- `object changed/changed`：`types.Object` 在 GC 后失效，属于对象存活/栈映射问题；
- `type switch ...`：隐式变量语义与当前 Go 版本不符；
- 崩溃或超时：遍历死循环或路径切片越界。

## 参数

- `UAF_SEED`：每轮 GC 后打乱目标标识符抽查顺序；失败信息带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_ast_path_defuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
