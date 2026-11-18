---
id: l16_html_nested_context
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/exec_test.go:830 html/template 执行与上下文自动转义
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:36 HTML/URL/JS/CSS 各上下文转义表
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/multi_test.go:266 空 block 替换结果受模板名注册顺序影响（Issue19294）
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:73 嵌套 base 模板共享 URL 上下文转义
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:91 URL 中动态分支导致 ambiguous context
tags: [L16, templates, html-template, nested-template, block, context-propagation, safe-types, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

同一 `frag` 片段被 `template` 调用在 HTML 文本、URL 查询、JS 字符串、CSS 字符串与属性位置，验证 html/template 按调用点传播转义上下文；`layout` 再嵌套 `body/link/script/style/card`，覆盖 range 属性、`<div {{.SafeAttr}}>`、动态 URL 过滤与 `URL/HTMLAttr/JSStr/CSS/Srcset` 安全类型。另测 `block` 默认值在两种解析顺序下的覆盖结果，以及未闭合 JS/属性上下文在执行期的错误。对应 Hugo 的上下文转义/block 顺序与 gitea 的嵌套 URL 上下文。

# 期望行为

1. `layout` 输出逐字节匹配：class 属性转义引号，正文引号转 `&#34;/&#39;`、`<>&` 转义，range 的 `<`/`&` 转义；URL 查询 `/&=/空格` 转 `%2f%26%3d%20`；JS 字符串输出 `<...`；CSS 输出 `\3c ...\26 `；card 输出 HTML 文本转义；
2. 单独执行 body/link/script/style/card 的输出与 layout 中对应片段完全一致；
3. 属性内 range 输出 `data-x="a&lt;b,c&amp;d,"`；
4. 动态 `javascript:` URL 变为 `#ZgotmplZ`，`template.URL` 保留为 `javascript:alert%281%29`；HTMLAttr 原样插入，JSStr 输出 `"<\/script>`，CSS 原样，Srcset 原样，动态属性名变 `ZgotmplZ`；
5. block：先解析 layout 再解析 footer 覆盖得 `OV`；先解析覆盖再解析 layout（block 默认后注册）得 `DEFAULT`；
6. 未闭合 JS 与未闭合属性模板在首次 Execute 时返回 `ends in a non-text context`；URL 中条件分支返回 `ambiguous context`；缺失子模板执行期返回 `no such template "nope"`；
7. `TORTURE_ROUNDS`（默认 20）轮随机顺序重放全部片段，每 4 轮 GC；GC 后 layout 输出与数据字段不变；
8. 末行输出 `ALL PASS l16_html_nested_context`。

# 失败判读

- `FAIL: layout output`：嵌套 template 上下文传播或转义规则异常；
- `FAIL: ... output`：某个上下文（URL/JS/CSS/属性）转义表变化；
- `FAIL: safe-*`：安全类型绕过/过滤逻辑与预期不符；
- `FAIL: override after layout`/`layout after override`：block 默认值与覆盖注册顺序错误；
- `FAIL: unclosed-*`：上下文状态机未检测未闭合；
- GC 轮次失败：模板树/转义缓存跨 GC 失效。

# 参数

- `TORTURE_ROUNDS`：默认 20，放大做长压测（上限 50000）；
- `UAF_SEED`：决定每轮片段执行顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_html_nested_context/*.go
TORTURE_ROUNDS=300 UAF_SEED=4 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
