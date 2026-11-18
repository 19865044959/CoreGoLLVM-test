---
id: l16_html_context_edge
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:206 事件处理器 onclick 的 JS 转义
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:568 HTML 注释中的动作处理
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:1206 TestEscapeText 跨上下文转义表
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:2077 TestPipeToMethodIsEscaped 管道结果按上下文转义
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/url_test.go:93 TestSrcsetFilter
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/css_test.go:154 TestCSSEscaper
tags: [L16, templates, html-template, escaping, rcdata, css-url, js-context, srcset, urlquery, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

模拟 Hugo 渲染复杂页面时容易踩坑的边缘上下文：`<textarea>/<title>` 的 RCDATA、
HTML 注释（动作会被剥除）、事件处理器属性里的 JS、`<style>` 中的 CSS `url()/@import`、
SVG `xlink:href`、`<meta refresh>` content、`<iframe srcdoc>`、`srcset`、未加引号属性，
以及 `printf/urlquery` 管道结果进入不同上下文后的转义。每个模板独立解析执行并逐字节比对。

## 期望行为

1. RCDATA（textarea/title）中的文本按 HTML 实体转义，注释内动作被整体移除、注释后的动作正常转义；
2. `onclick="alert({{.Text}})"` 按 JS 字符串上下文转义（`<`、`\&#34;` 等）；
3. CSS `url()`/`@import`、`xlink:href`、srcset、未加引号属性分别使用 CSS/URL/属性规则；
4. `<meta refresh>` content 只做 HTML 属性转义，不对 URL scheme 做过滤；
5. `<script type="application/json">` 与 `<script>` 注释使用 JS 上下文规则；
6. `SafeURL/SafeHTML` 经类型标记后不再二次转义，但 URL 仍规范化为百分号编码；
7. `printf`/`urlquery` 管道结果仍按目标上下文转义（`%3Cb%3E...`、`a&#43;b...`）；
8. `javascript:` URL 在 href/srcset/style 中被替换为 `#ZgotmplZ`；
9. 未闭合 script/属性上下文与歧义 URL 在 Execute 时报对应错误；
10. 3 轮 GC 后按种子打乱重放全部模板，输出逐字节一致，数据未被破坏。

## 失败判读

- `output= ... want=`: 上下文识别错误或转义规则偏离 Go 1.24 的 html/template；
- `parse err=` / `exec err=`: 模板解析器/转义器状态机错误；
- `round ... output=`：模板树或安全类型值在 GC 后失效/被修改；
- 超时：上下文解析在特殊属性名/注释上循环。

## 参数

- `UAF_SEED`：控制每轮 GC 后模板重放顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_html_context_edge/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
