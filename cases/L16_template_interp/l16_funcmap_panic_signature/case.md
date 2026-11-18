---
id: l16_funcmap_panic_signature
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1735 函数/方法 panic 被包装成执行错误
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1796 call 参数个数与可赋值性检查
  - gitea@v1.23.5: modules/templates/util_test.go:15 变参 FuncMap 的类型检查与错误路径
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:81 栈状态/参数错误的固定错误串
  - hugo@v0.155.0: tpl/templates/templates_integration_test.go:99 try 包装模板函数错误
tags: [L16, templates, FuncMap, panic, invalid-signature, nil-function, variadic, error-text, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

FuncMap 注册普通函数、返回 `(T, error)` 的函数、变参函数、会 panic（字符串/error）的函数和 typed nil 函数。模板覆盖成功路径、函数返回错误、panic 包装、nil 函数调用、参数不足/过多、参数类型错误与变参类型错误；另测 `Funcs` 注册阶段的非法签名（第二返回值非 error、返回 0/3 个值）和非函数值 panic。对应 Hugo 的 panic 包装与 call 参数检查测试。

# 期望行为

1. 成功模板输出 `5|3|6`；
2. `safeDiv(1,0)`、`boomString`/`boomError` panic、nil 函数调用均使 Execute 返回非 nil error，错误分别含 `error calling safeDiv: div-zero`、`error calling boomString: kaboom-fn`、`error calling boomError: boom-err`、`call of nil function`；错误点后的静态文本不输出；
3. 参数不足/过多含 `wrong number of args for add: want 2 got 1/3`；参数类型错误含 `expected integer; found "s"`（变参同）；
4. `Funcs` 对第二返回值非 error、0/3 个返回值、非函数值分别 panic，文本含 `second return value should be error; is string`、`has 3 return values; should be 1 or 2`、`has 0 return values; should be 1 or 2`、`value for ... not a function`；
5. 失败的 `Funcs` panic 后，原模板与 `Clone` 仍可执行并输出 `1`；
6. 所有错误文本在重复执行与 GC 后逐字节稳定；
7. 最后一行 `ALL PASS l16_funcmap_panic_signature`。

# 失败判读

- `expected error, got nil`：panic/error 被模板引擎吞掉；
- `error ... missing ...`：错误包装或参数校验规则与标准库不一致；
- `Funcs did not panic`：非法签名/非函数值被接受；
- `base/clone after failed Funcs`：注册失败破坏了已有函数表；
- GC 后错误文本变化或崩溃：模板内部函数表/错误字符串被破坏。

# 参数

- `UAF_SEED`：决定重复轮数（8–12）与检查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_funcmap_panic_signature/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GODEBUG=clobberfree=1 /tmp/case
```
