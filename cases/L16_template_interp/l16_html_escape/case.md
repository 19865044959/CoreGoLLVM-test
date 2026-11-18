---
id: l16_html_escape
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/exec_test.go:830 html/template 执行与自动转义
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:36 HTML/URL/JS/CSS 各上下文转义表
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:73 嵌套模板 URL 上下文转义
tags: [L16, templates, html-template, escaping, contexts, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

同一份 `html/template` 同时覆盖 HTML 文本、带引号属性、URL 查询串、JS 字符串与指针字段五个上下文，执行后与逐字节 golden 输出比较；每轮再分配垃圾并 GC，重新执行同一模板校验转义缓存与模板树跨 GC 有效。对应 Hugo 内嵌 htmltemplate 的上下文自动转义测试。

# 期望行为

1. HTML 文本 `<b>hi & bye</b>` 输出 `&lt;b&gt;hi &amp; bye&lt;/b&gt;`；
2. 属性中的 `x"<y>` 输出 `x&#34;&lt;y&gt;`；
3. URL 查询值 `a b&c/d?` 输出 `a%20b%26c%2fd%3f`；
4. JS 字符串中的 `a"b<c>` 被转义：引号前加反斜杠，`<`/`>` 变成 Unicode 转义序列，原始 `<` 不出现；
5. 指针字段 `*string` 指向的 `<i>p</i>` 被文本上下文转义；
6. GC 后再次执行输出与 golden 完全一致，最后一行 `ALL PASS l16_html_escape`。

# 失败判读

- `FAIL: round N output`：上下文识别或转义规则与预期不符（逐字节 diff 已打印）；
- `FAIL: ... raw HTML leaked`：文本上下文未转义，存在 XSS 风险；
- GC 后 golden 变化：模板树/转义上下文缓存被破坏（写屏障或指针标记问题）；
- 超时：`TORTURE_ROUNDS` 设置过大。

# 参数

- `TORTURE_ROUNDS`：默认轮数 30–34，设为更大值做长压测（上限 200000）；
- `UAF_SEED`：微调默认轮数，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_html_escape/*.go
TORTURE_ROUNDS=200 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=clobberfree=1 /tmp/case
```
