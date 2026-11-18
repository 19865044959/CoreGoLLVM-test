---
id: l16_pipeline_clone_bind
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:184 TestFuncMapWorksAfterClone
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:271 TestClonePipe
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:169 TestCloneThenParse
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1185 TestFinalForPrintf
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1528 TestBlock
tags: [L16, templates, pipeline, clone, FuncMap, option-isolation, TORTURE_ROUNDS, GC]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

模拟 Hugo 模板缓存：一个 base 模板被反复 `Clone`，每个 Clone 绑定不同的 FuncMap、
覆写 `row` 局部模板、新增管道模板，然后并发/重复执行。重点验证 Clone 后的函数表扩展
只影响该 Clone、对关联模板调用 `Funcs` 会作用于整个 Clone 命名空间、Option 与模板
命名空间相互隔离、管道短路/错误传播与 base 的稳定性。

## 期望行为

1. base 的 `main/pipe` 管道输出与 golden 一致（`<go>|[go:2:GO][tmpl:6:10][gc:8:14]`、`12|005`）；
2. Clone 后新增 `tag` 函数与 `tagged` 模板可执行，base 解析 `tag` 报 `function "tag" not defined`；
3. 对 Clone 的 `row` 模板调用 `Funcs` 后，Clone 新模板能使用 `mark`；
4. Clone 默认不继承 `missingkey=error`，Clone 上设置 Option 不影响 base；
5. base 在 Clone 之后新增的 `late` 函数不进入已有 Clone；
6. 管道中的函数错误被包装为带模板名/行号的执行错误（`pipeline boom 6`）；
7. 每轮 TORTURE_ROUNDS 都重新 Clone、覆写 `row`、执行随机顺序的 row/round 管道，base 输出不变；
8. 每 4 轮及最终执行显式 GC，GC 后 base/Clone 管道输出仍一致。

## 失败判读

- `base parse err=/clone ... err=`：FuncMap 绑定顺序或 Clone 隔离语义错误；
- `round ... out=`：管道求值顺序（参数在管道尾部）或函数表共享错误；
- `base row changed`：Clone 的覆写泄漏回 base，模板树/命名空间别名错误；
- `post-GC ... changed`：模板树或闭包在 GC 后失效；
- 超时：TORTURE_ROUNDS 过大或 Clone 链无限增长。

## 参数

- `UAF_SEED`：每轮随机执行顺序与 Clone 变量选择的种子；失败输出带种子；
- `TORTURE_ROUNDS`：放大轮数（默认 20，上限 50000）；每轮 Clone + 覆写 + 执行多个管道模板。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_pipeline_clone_bind/*.go
UAF_SEED=1 TORTURE_ROUNDS=200 GODEBUG=clobberfree=1 /tmp/case
```
