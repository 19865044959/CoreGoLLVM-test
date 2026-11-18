---
id: l16_css_js_url_escape
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:36 各上下文转义表
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/exec_test.go:830 html/template 执行与自动转义
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:88 URL query 中 `/` 转义为 `%2f`
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:97 URL 中动态分支导致 ambiguous context
tags: [L16, templates, html-template, css-escape, js-escape, url-escape, srcset, safe-types, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

同一份 `html/template` 覆盖 CSS 值与 url()、JS 字符串与 RegExp、href 危险协议、URL 路径与查询串、srcset 多候选，以及 `template.URL/JS/CSS/HTML` 安全类型。三组输入（含 `javascript:`、`expression()`、`</script>`、引号/反斜杠/换行）与 Go 1.24.1 的逐字节 golden 比较；每轮解析模板、执行、GC 后再执行，验证转义缓存与模板树跨 GC 稳定。

# 期望行为

1. 不安全 CSS 值/`javascript:` URL 被替换为 `ZgotmplZ`，计数分别为 4/1/0（三组输入）；
2. JS 字符串中 `</script>`、引号、`<`/`>` 被转义为 `<\/script>`、`"`、`<` 等，原始闭合标签不出现；
3. URL 路径保留 `/`，查询串完整百分号编码（`a%20b%2fc%3fd%26e%3df%23g`），属性中的 `&` 变为 `&amp;`；
4. srcset 中不安全候选被 `#ZgotmplZ` 替换，安全候选保留；
5. `template.URL/JS/CSS/HTML` 按声明原样输出（仅属性级实体转义）；
6. GC 后同一模板对同一数据的输出与 golden 完全一致；
7. 最后一行 `ALL PASS l16_css_js_url_escape`。

# 失败判读

- `FAIL: ... output`：上下文识别/转义规则与标准库不一致（逐字节 diff 已打印）；
- `ZgotmplZ count` 不符：安全过滤器（CSS value/URL scheme/srcset）行为异常；
- `raw JS close tag leaked`：JS 上下文未转义，存在 XSS 风险；
- GC 后 golden 变化/崩溃：模板转义树或字符串数据被破坏；
- 超时：`TORTURE_ROUNDS` 过大。

# 参数

- `TORTURE_ROUNDS`：默认 12–18 轮，可放大做长压测（上限 200000）；
- `UAF_SEED`：决定轮数、输入变体顺序与随机源，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_css_js_url_escape/*.go
TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=clobberfree=1 /tmp/case
```
