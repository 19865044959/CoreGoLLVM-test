---
id: l16_positions
dim: L16_template_interp
priority: P0
sources:
  - golangci-lint@v2.0.2: pkg/result/processors/nolint_filter_test.go:47 行号 → nolint 指令区间匹配
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:16 诊断的 Pos/SuggestedFix 位置计算
  - gitea@v1.23.5: modules/templates/htmlrenderer_test.go:18 根据模板错误行列在源码上画 ^^^ 指示
tags: [L16, goast, go-token, positions, line-directive, GC]
requires: [goast]
runtime: "<3s"
---

# 场景

在同一个 `token.FileSet` 中按 `UAF_SEED` 随机顺序解析三个源码文件（普通文件、第二文件、带 `//line fake.go:100:7` 行指令的文件），校验标识符行/列/字节偏移、`token.File.LineStart`/`LineCount`、文件 Base 区间、`PositionFor(adjusted)` 与 UTF-8 多字节列的字节语义；GC 后重复全部位置查询。对应 golangci-lint 的行号匹配与 gitea 错误行列定位。

# 期望行为

1. `a.go` 中 `Alpha` 位于 3:7/offset 24、`Beta` 4:5、`Gamma` 5:6；字符串字面量 `"中文标识"` 位于 8:13，结束列 27（按字节计，含 12 字节 UTF-8 内容）；
2. 每个文件的 `LineCount` 与 `LineStart(i)` 和按换行计算的偏移表一致，末个声明结束偏移等于去掉尾部换行后的源码长度；
3. 三文件 Base 互不重叠，`fset.File(base)`/`fset.File(ast.Pos())` 指回同一 `token.File`；
4. `c.go` 中 `V` 的原始位置为 `c.go:3:5`，`PositionFor(pos,true)` 为 `fake.go:100:11`，默认 `Position` 使用调整后位置；
5. GC 后所有行/列/offset 查询结果不变；
6. 最后一行 `ALL PASS l16_positions`。

# 失败判读

- `FAIL: ... pos=... want ...`：行/列/offset 映射错误，优先检查 token.File 行表或列是字节还是 rune 的语义；
- `FAIL: LineCount/LineStart`：行表构建错误（常见于尾部换行处理）；
- `FAIL: V adjusted pos`：`//line` 指令解析或 adjusted 位置计算错误；
- GC 后失败：FileSet/token.File 的堆结构被破坏。

# 参数

- `UAF_SEED`：决定三个文件的解析顺序（影响 Base 分配），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_positions/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
