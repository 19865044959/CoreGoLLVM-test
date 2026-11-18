---
id: l14_wrapped_panic_repanic_hooks
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:99 - 第一次 panic：wpErr(sentinel1)
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:106 - defer 内第二次 panic：fmt.Errorf("%w") 包装新 wpErr
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:115 - wpRun 的双 defer recover/re-panic 与抓栈
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:169 - errors.Is 钩子调用计数
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:181 - errors.As 自定义钩子
  - cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/main.go:196 - traceback 保留两次 panic
  - docs/DIMENSIONS.md:22 - L14 调试信息（panic 栈/错误链）
tags: [L14, wrapped-error, errors.Is, errors.As, repanic, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

第一次 panic 携带自定义 `wpErr`（tag=first，sentinel1，special1）。内层 defer recover 后不再返回，而是立即 panic 一个 `fmt.Errorf("wp-second-wrap: %w", mid)`，其中 `mid` 是第二层 `wpErr`（tag=second，sentinel2，special2），其 `inner` 链回一个 sentinel1 的 `wpErr`。外层 defer 最终 recover 到第二层错误并抓 `debug.Stack()` 与 `runtime.CallersFrames`。验证 re-panic 后的错误链、Is/As 钩子与两次 panic 的 traceback 历史。

## 期望行为

1. 最终 recover 值实现 error，文本含 `wp-second-wrap` 与 `wp:second`，且不是第一次 panic 的对象；
2. `errors.Is(final, sentinel2)` 为 true，第二层自定义 Is 调用计数恰好 +1；
3. `errors.Is(final, sentinel1)` 为 true：第二层 Is 返回 false 后沿 inner 链命中，计数恰好 +2；
4. `errors.Is(final, sentinel3)` 为 false，但仍依次调用两层 Is（计数 +2）；
5. `errors.As(final, &sp)` 走第二层自定义 As（计数 +1），返回的 `*wpSpecial` 与 second-special 指针同一、id/name 正确；
6. `debug.Stack()` 同时含 `main.wpFirst(`、`main.wpSecond(`、`main.wpRun.func1(`、`main.wpRun.func2(`、`main.wpRun(`，并至少出现两个 `panic(` 帧；帧表含 wpFirst/wpSecond/wpRun 且 File/Line/Entry 可解析；
7. 显式 GC 后 Is/As 仍命中同一错误链与 special 指针，payload 链有效；最后一行 `ALL PASS l14_wrapped_panic_repanic_hooks`。

## 失败判读

- 最终错误仍是第一次对象：内层 defer 未成功 re-panic；
- sentinel1 不命中：二次 panic 的 inner 链丢失；
- Is 计数少于预期：errors.Is 未沿 fmt 包装链调用自定义钩子；
- `errors.As` panic `*target must be interface or implement error`：目标类型未实现 error；
- traceback 只含第二次 panic：re-panic 时旧 panic 栈被错误丢弃（本用例如实观察到两次 panic 帧）。

## 参数

无随机路径。断言比较钩子调用次数的精确增量、错误链文本与指针身份，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_wrapped_panic_repanic_hooks/*.go
GODEBUG=clobberfree=1 /tmp/case
```
