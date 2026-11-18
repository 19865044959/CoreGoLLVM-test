---
id: l16_funcmap_error
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:86 ErrFunc 字段类型 func() (string, error)
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:176 ErrFunc 返回 (值, nil) 的成功路径
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:655 error 方法返回非 nil error 时执行中止
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:99 try 包装模板函数错误
  - gitea@v1.23.5: modules/templates/util_test.go:78 FuncMap 错误被包装为 error calling ...
tags: [L16, templates, FuncMap, error-propagation, nested-template, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

FuncMap 中注册 `div/mk/nameOf/total` 等返回 `(T, error)` 的函数，覆盖：成功值参与管道与 `printf`、返回 `(nil, nil)` 的指针、除零/空名/nil 指针/负数的错误、嵌套 `define/template` 中的错误传播、错误后不再执行后续动作。每轮把接收者指针交给 FuncMap 并执行模板，GC 后直接调用函数与再次执行模板，校验错误文本与对象身份稳定。

# 期望行为

1. 成功模板输出形如 `5|x:7|6|i|005`，`div` 结果可直接进 `printf "%03d"`；
2. 五类错误均返回非 nil error，包含 `div: divide by zero`、`mk: empty name`、`name: nil item`、`total: negative value` 等固定文本与 `executing` 上下文；
3. 错误发生后不写出错误点之后的静态文本/动作（TAIL、post、X 不出现）；
4. `mk "nil"` 返回 `(nil, nil)`：with 走 else，直接输出 `<nil>`；
5. 同一模板的错误文本在重复执行与 GC 后逐字节稳定；
6. 最后一行 `ALL PASS l16_funcmap_error`。

# 失败判读

- `FAIL: ... expected execute error, got nil`：函数错误被模板引擎吞掉；
- `error ... missing ...`：错误文本/包装规则与标准库不一致；
- `executed past the error`：错误后仍执行后续动作，控制流异常；
- GC 后函数接收者字段错乱/崩溃：传入模板的指针对象被破坏；
- 超时：错误被吞导致大循环（不应发生）。

# 参数

- `UAF_SEED`：决定轮数（8–12）与数据增量，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_funcmap_error/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=efence=1 /tmp/case
```
