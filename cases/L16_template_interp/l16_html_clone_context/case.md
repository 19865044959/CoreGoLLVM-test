---
id: l16_html_clone_context
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:21 TestAddParseTreeHTML
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:169 TestCloneThenParse
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:205 TestTemplateCloneExecuteRace
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:251 TestCloneRedefinedName
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:271 TestClonePipe
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/escape_test.go:2114 TestIdempotentExecute
tags: [L16, templates, html-template, clone, addparsetree, escaping, nested-context, concurrency, TORTURE_ROUNDS, GC]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

模拟 Hugo 对同一套 HTML 模板做多语言/多主题变体：base 模板含 `<title>`、URL 查询、
`<script>`、`<style>` 等多种上下文且保持未执行，之后反复 `Clone`、覆写 `head`、用
`AddParseTree` 注入片段，再并发执行。验证 html/template 的上下文转义在克隆/子树拼接后
仍按调用点生效、Clone 命名空间互相隔离、执行过的模板不能再 Clone。

## 期望行为

1. 通过 Clone 执行 base 的 head/body/layout/frag，输出与 golden 逐字节一致；
2. Clone 覆写 `head` 为 `<h1>` 后重新按 HTML 文本上下文转义，base 保持原输出；
3. Clone 后 Parse 的 `extra` 只在 Clone 可见，base 执行该名字报错；
4. `AddParseTree` 注入的 `b` 在 `<a href>` 调用点按 URL 上下文转义，且执行前 Clone 成功、输出一致；
5. 不完整的 `lhs/rhs` 上下文分别由不同 Clone 补成 URL/JS 上下文，输出各自正确；
6. 已执行模板的 `Clone()` 返回 `cannot Clone ... after it has executed`；
7. 未闭合的 `<script>` 在 Execute 时报 `non-text context`；
8. 安全类型 `template.URL` 在 Clone 后仍过滤为 `javascript:alert%281%29`；
9. 每轮 TORTURE_ROUNDS 重新 Clone + 覆写 head/round，随机顺序执行 layout/body/frag/round 均正确；
10. 4 个 Clone 并发执行 16 次输出一致；最终 GC 后 base 仍可 Clone 且输出不变。

## 失败判读

- `... err=`：Clone/AddParseTree 后上下文状态或模板命名空间错误；
- `layout=/body=`：转义上下文在 Clone 后丢失或过度转义；
- `cannot Clone`：执行状态标记错误；
- `concurrent ... out=`：并发执行共享状态被污染；
- `post-GC ...`：模板树/上下文在 GC 后失效；
- 超时：TORTURE_ROUNDS 过大或 Clone 链无界增长。

## 参数

- `UAF_SEED`：每轮随机执行顺序与并发交错种子；失败输出带种子；
- `TORTURE_ROUNDS`：放大轮数（默认 16，上限 50000）；每轮 Clone + 覆写多上下文模板并执行。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_html_clone_context/*.go
UAF_SEED=1 TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
```
