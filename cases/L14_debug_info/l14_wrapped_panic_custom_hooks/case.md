---
id: l14_wrapped_panic_custom_hooks
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_wrapped_panic_custom_hooks/main.go:48 - 自定义 Is 钩子
  - cases/L14_debug_info/l14_wrapped_panic_custom_hooks/main.go:53 - 自定义 As 钩子
  - cases/L14_debug_info/l14_wrapped_panic_custom_hooks/main.go:97 - 两层 fmt.Errorf("%w") 包装
  - cases/L14_debug_info/l14_wrapped_panic_custom_hooks/main.go:167 - errors.Is 钩子断言
  - docs/DIMENSIONS.md:22 - L14 调试信息（panic 栈）
tags: [L14, wrapped-error, errors.Is, errors.As, panic, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

panic 值经两层 `fmt.Errorf("%w")` 包装，最内层自定义 `cwErr` 同时实现 `Unwrap`、`Is`、`As` 钩子。recover 后 `errors.Is`/`errors.As` 必须穿过包装链调用钩子；即使目标不匹配，钩子也应被调用。traceback 保留 panic 路径上的真实符号与行号。对应服务把多个下游错误包装后 panic、崩溃日志需要同时还原错误链与代码位置的场景。

## 期望行为

1. recover 值实现 error，错误文本同时包含 `cw-outer`、`cw-mid`、`cw-core:wrapped-panic`；
2. 两次 `errors.Unwrap` 得到最内层 `*cwErr`，其 payload 指针与 panic 时同一，`special` 指针非空；
3. `errors.Is(got, cwTarget)` 为 true 且递增自定义 Is 计数；对不相关目标返回 false 但仍调用 Is；对新构造的错误也不命中；
4. `errors.As(got, &sp)` 为 true，递增自定义 As 计数，取出的 `*cwSpecial` 与最内层保存的指针同一；
5. traceback 含 `main.cwPanicLeaf(`、`main.cwBoom(`、`main.cwRun(`、goroutine 头与 `main.go:<行号>`；
6. 上述三个帧 File 以 `main.go` 结尾、Line>0，Entry 反查与帧名一致；
7. GC 后 payload 链、special 指针与 Is/As 钩子仍有效，最后一行 `ALL PASS l14_wrapped_panic_custom_hooks`。

## 失败判读

- `errors.Is/As` panic 或未命中：包装链 Unwrap 或钩子签名错误（As 的目标类型必须实现 error）；
- 钩子计数不增加：errors 包未沿包装链调用自定义 Is/As；
- recover 值不是 error 或 payload 身份不符：panic 值类型/包装错误；
- traceback 行号为 0：panic 路径行号表缺失。

## 参数

无随机路径。断言比较钩子调用次数是否增加、指针身份与相对行号，不依赖具体计数和行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_wrapped_panic_custom_hooks/*.go
GODEBUG=clobberfree=1 /tmp/case
```
