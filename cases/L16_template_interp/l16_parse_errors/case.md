---
id: l16_parse_errors
dim: L16_template_interp
priority: P0
sources:
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:234 parse 错误包含模板名与行号
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:250 execute 错误包含模板名与行号
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:97 URL 混合上下文报 ambiguous context
  - gitea@v1.23.5: modules/templates/htmlrenderer_test.go:18 从模板错误中提取行号并生成指示
  - gitea@v1.23.5: modules/templates/htmlrenderer_test.go:56 各类 parse/execute 错误格式化
tags: [L16, templates, parse-error, error-line, html-template, escape-context, GC]
requires: [templates]
runtime: "<2s"
---

# 场景

10 个 text/template 解析错误（未闭合 action/注释/if、意外 `{{end}}`、未定义函数、缺 pipeline、坏数字、define 缺名）检查 `template: name:line:` 前缀、行号与固定错误片段；`template.Must` 必须 panic 同一错误。7 个 html/template 用例 Parse 成功但首次 Execute 报转义上下文错误（URL ambiguous、非文本上下文、range 分支上下文、未加引号属性、管道中 urlquery、缺失子模板），错误在重复执行与 GC 后保持稳定。

# 期望行为

1. 每个 text 错误非 nil，含 `template: <name>:`、预期行号与固定片段（如 `unclosed action started at unclosed_action:2`、`function "nosuchfunc" not defined`）；
2. 解析失败时返回的模板为 nil；`template.Must` panic 值转字符串与 Parse 错误完全一致；
3. 每个 html 用例 Parse 无错误、Execute 返回非 nil，输出为空，错误含模板名及 `ambiguous context within a URL`、`ends in a non-text context`、`branches end in different contexts`、`"=" in unquoted attr`、`predefined escaper "urlquery" disallowed`、`no such template "foo"`；
4. 同一 html 模板第二次 Execute 的错误文本与第一次逐字节相同；
5. GC 后重新解析/执行，错误文本仍与预期一致；
6. 最后一行 `ALL PASS l16_parse_errors`。

# 失败判读

- `expected parse error, got none`：非法模板被接受，语法检查退化；
- `lacks line`：错误位置映射错误（token.File/行表问题）；
- `Must panic != Parse error`：`template.Must` 包装路径异常；
- `Parse unexpectedly failed`：转义上下文检查提前到 Parse，与 Go 1.24 行为不符；
- `wrote output before escape error` / 错误不稳定：转义分析在输出后或缓存被破坏；
- GC 后错误变化/崩溃：模板内部字符串/节点被破坏。

# 参数

- `UAF_SEED`：决定轮数（3–5）与两类错误用例的检查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_parse_errors/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=efence=1 /tmp/case
```
