---
id: l16_vars_expand_parse
dim: L16_template_interp
priority: P0
sources:
  - gitea@v1.23.5: modules/templates/vars/vars_test.go:12 TestExpandVars 花括号变量展开
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:926 TestDelims 自定义/保留动作定界
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1425 TestUnterminatedStringError 未闭合字符串错误
tags: [L16, templates, vars-expand, text-template, brace-expansion, injection, error-position, GC]
requires: [templates]
runtime: "<3s"
---

# 场景

模拟 gitea 的模板变量预处理：先把 `{name}` 展开成配置值，再交给 `text/template`
解析执行。两阶段之间有两个边界：`{{...}}` 动作必须保留，动作内引号/注释里的花括号
不能被误展开；展开值本身可能包含模板动作甚至引号，注入后会改变解析结果或触发解析错误。
本用例同时检查多行内容中的字节位置与模板错误行号。

## 期望行为

1. 纯文本变量展开后原样执行：`Hello {name}, n={n}` → `Hello world, n=7`；
2. `{{if {cond}}}...{{end}}` 的动作内未加引号变量被展开，结果为 `world`；
3. 展开值 `{{.N}}`/`{{if true}}ok{{end}}` 注入后由模板引擎解释，得到 `Value=7`/`R=[ok]`；
4. 动作内双引号字符串与注释中的 `{name}` 不展开（`"a{b}"`、注释用例输出不变）；
5. Unicode 变量名与值正确展开，值中的 `a{b}` 不会被二次展开；
6. 未定义变量在展开阶段报错、原文保留，多行输入报出正确字节位置（byte 6）；
7. 引号注入产生 `unterminated quoted string`，错误行为 2；未闭合动作注入产生 `b:2` 解析错误；
8. 未闭合 `{{` 或 `{name` 在展开阶段报错；
9. 同一次 `expand` 不递归展开替换值，第二次展开才生效（两阶段语义）；
10. 3 轮 GC 后按种子打乱重放全部成功用例，输出/错误与首次一致。

## 失败判读

- `out= ... want=`：展开器状态机（动作/引号/注释）处理错误；
- `undefined var` / `unclosed`：错误检测或错误文本不稳定；
- `quote parse err=` / `b:2:`：两阶段错误行号或注入边界与 `text/template` 不符；
- `round ... out=`：保留的展开结果/模板在 GC 后失效；
- 超时：扫描器在未闭合状态死循环。

## 参数

- `UAF_SEED`：控制每轮 GC 后成功用例重放顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_vars_expand_parse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
