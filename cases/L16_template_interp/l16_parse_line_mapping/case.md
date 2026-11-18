---
id: l16_parse_line_mapping
dim: L16_template_interp
priority: P0
sources:
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:234 parse 错误包含模板名与行号
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:250 execute 错误包含模板名与行号
  - gitea@v1.23.5: modules/templates/htmlrenderer_test.go:18 根据模板错误行列在源码上画 ^^^ 指示
  - golangci-lint@v2.0.2: pkg/goanalysis/pkgerrors/extract_test.go:8 从嵌套错误文本提取最深 file:line:col
tags: [L16, templates, parse-error, line-mapping, crlf, unicode, template-must, execute-error, GC]
requires: [templates]
runtime: "<3s"
---

# 场景

10 个多行模板分别覆盖：未闭合 action/注释、缺 if/range 值、未定义函数、意外 `{{end}}`、空 action、EOF、以及 CRLF、UTF-8 中文/重音字符、`define` 内部的错误。逐个检查 `template: <name>:<line>:` 前缀、精确行号与固定错误片段，并验证 `template.Must` panic 文本与 Parse 错误一致。另测一次失败的 `Parse` 不破坏同一集合中已注册模板，以及执行期缺失子模板错误的行号与部分输出。

# 期望行为

1. 每个 parse 错误非 nil、返回的模板为 nil，错误含模板名、预期行号与固定片段（如 `unclosed action started at unclosed:2`、`function "nosuch" not defined`）；
2. CRLF 只按 `\n` 计行，Unicode 前缀不影响行号；`define` 内部错误行号指向真实行；
3. `template.Must` panic 的字符串与 Parse 错误逐字节相同；
4. 对已有模板集合执行失败 Parse 后，原有 `good`/`root` 模板仍可执行；
5. 执行期 `{{template "nope"}}` 错误含 `exec:2:` 与 `not defined`，错误前已写出的 `l1\n` 保留；
6. GC 后重新解析的所有错误文本与首次一致，已存模板仍可执行；
7. 最后一行 `ALL PASS l16_parse_line_mapping`。

# 失败判读

- `failed parse returned non-nil template`：Parse 失败路径返回值异常；
- `missing line N`：行表/换行统计错误（CRLF/多字节文本）；
- `Must panic != Parse error`：Must 包装路径异常；
- `good template after failed Parse`：失败 Parse 破坏了模板集合；
- `execute error missing line`：执行期位置映射丢失；
- GC 后错误变化：错误字符串/模板节点被破坏。

# 参数

- `UAF_SEED`：决定 2–3 轮中错误用例的检查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_parse_line_mapping/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=clobberfree=1 /tmp/case
```
