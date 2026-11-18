---
id: l16_html_vs_text_escape
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/exec_test.go:830 html/template 执行与自动转义
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:36 HTML/URL/JS/CSS 各上下文转义表
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:73 嵌套模板 URL 上下文转义
  - gitea@v1.23.5: modules/templates/helper_test.go:60 jsEscapeSafe 字节映射
tags: [L16, templates, html-template, text-template, auto-escaping, safe-types, url-filter, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

同一段包含 HTML 文本、引号属性、URL 查询、危险 `javascript:` URL、JS 字符串、srcset、CSS 与 `template.HTML/URL/JS/CSS` 安全类型的模板，分别由 `text/template` 和 `html/template` 执行。text 端原样输出所有字符串；html 端按上下文转义、过滤危险 URL，并信任安全类型。用于直接对比两套引擎的转义边界，对应 Hugo 内嵌 htmltemplate 的转义测试。

# 期望行为

1. text 输出逐字节等于 golden：`<b>&"`、`javascript:alert(1)`、`<i>plain</i>` 全部原样，不出现 `&lt;`/`#ZgotmplZ`；
2. html 输出逐字节等于 golden：文本/属性转义为 `&lt;b&gt;&amp;&#34;`，URL 查询转义为 `a%20b%26c%2fd%3f`，危险 URL 变为 `#ZgotmplZ`，JS 字符串加引号并 `<` 转义；
3. html 对普通字符串 `<i>plain</i>` 转义，但 `template.HTML` 的 `<i>ok</i>`、`template.CSS`/`JS`/`URL` 原样输出；
4. 两端输出不相等，且重复执行逐字节一致；
5. GC 后安全类型值不变，两个模板再次执行仍等于各自 golden；
6. 最后一行 `ALL PASS l16_html_vs_text_escape`。

# 失败判读

- `html/text output` 差异：上下文转义状态机或安全类型判定错误；
- `html leaked ...`：危险 URL/原始 HTML/普通字符串未转义；
- `text escaped HTML` / `text filtered URL`：text/template 不该做任何转义；
- `Safe* mutated`：安全类型底层字符串被破坏；
- GC 后输出变化：转义模板树或字符串被回收/改写。

# 参数

- `UAF_SEED`：决定轮数（6–9）与 html/text 执行顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_html_vs_text_escape/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=clobberfree=1 /tmp/case
```
