---
id: l16_parser_recover_scope
dim: L16_template_interp
priority: P0
sources:
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 ParseFile(ParseComments) 解析测试内联源码
  - quic-go@v0.50.1: internal/qerr/errorcodes_test.go:20 解析源码并遍历 Decls/ValueSpec
  - tailscale@v1.80.2: cmd/viewer/viewer_test.go:41 parser.ParseFile + types.Info + Scope.Lookup 的联动
  - ent@v0.14.4: entc/integration/config/config_test.go:58 ParseComments + ast.Inspect 定位字段
tags: [L16, goast, go-parser, error-recovery, go-types, scopes, uses, GC]
requires: [goast]
runtime: "<3s"
---

# 场景

模拟 golangci-lint / quic-go 这类静态分析器在源码存在语法错误时的处理路径：`parser.AllErrors`
仍需保留错误前后的完整声明与残缺函数体，分析器过滤掉 `BadExpr/BadStmt` 后把可用声明
打印、重解析并交给 `go/types` 建立包/函数作用域，最后用 `Uses`/`LookupParent` 解析包级常量。

## 期望行为

1. 坏源码解析返回非空部分 AST，`Sum/UseBase/Broken/Double` 均仍可找到，`Broken` 位置正确；
2. 返回 `scanner.ErrorList`，按 offset 有序且至少 2 条错误；
3. 恰好 1 个声明含恢复插入的 Bad 节点，过滤后 5 个声明可打印为合法源码；
4. 重解析后 `types.Config.Check` 成功，包作用域恰为 `Base/Double/Sum/Total/UseBase`；
5. `UseBase` 中对 `Base` 的 `Uses` 与包级 `*types.Const` 是同一对象，函数作用域经 file scope 指向包作用域；
6. 修复源码后 `Broken` 的局部变量 `x` 正常进入 `Defs`；
7. 3 轮 GC 后包作用域对象身份、错误列表文本/位置、AST 打印结果均不变。

## 失败判读

- `FAIL: ...` 或退出码非 0：解析恢复边界或 go/types 对象身份与期望不符；
- `SIGSEGV` / `fatal error: found bad pointer`：AST/作用域对象在 GC 后失效，优先怀疑对象存活或栈映射；
- `type check recovered source:` 报错：过滤逻辑把残缺声明带入了 types 检查；
- 超时：AST 打印/类型检查路径出现死循环。

## 参数

- `UAF_SEED`：随机路径种子，控制每轮复核包级名字的顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_parser_recover_scope/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
