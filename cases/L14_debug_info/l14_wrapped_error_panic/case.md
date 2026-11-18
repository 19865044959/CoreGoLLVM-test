---
id: l14_wrapped_error_panic
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_wrapped_error_panic/main.go:31 自定义 Error
  - cases/L14_debug_info/l14_wrapped_error_panic/main.go:33 自定义 Unwrap
  - cases/L14_debug_info/l14_wrapped_error_panic/main.go:36 wrapLeaf 内 panic 包装 error
  - cases/L14_debug_info/l14_wrapped_error_panic/main.go:49 errors.Is/As + 抓栈
  - docs/DIMENSIONS.md:22 L14 调试信息（traceback）
tags: [L14, wrapped-error, errors.Is, errors.As, panic, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

panic 值是一个三层包装的 error：自定义 `wrapContext`（带 payload 指针与 Unwrap）包住两层 `fmt.Errorf("%w")`，最内层是 sentinel。recover 后必须能穿过 panic 值做 `errors.Is`/`errors.As`，traceback 保留包装函数链的真实符号。

## 期望行为

1. 静态 `fmt.Errorf` 双层 `%w` 链可被 `errors.Is` 找到 sentinel；
2. recover 到的 error 文本为 `leaf: stage2: stage1: sentinel-failure`；
3. `errors.Is(panicValue, sentinel)` 与 `errors.As(panicValue, *wrapContext)` 均成立，包装器里的 payload 指针与 panic 时同一对象；
4. traceback 包含 `main.wrapLeaf`、`main.wrapMid`、`main.wrapTop`、`goroutine` 头与至少 3 处 `main.go:<行号>`；
5. GC 后 payload 链仍有效；
6. 退出码 0，最后一行 `ALL PASS l14_wrapped_error_panic`。

## 失败判读

- `errors.Is/As` 失败：panic 值的 Unwrap 链在 recover 路径上损坏；
- 栈符号缺失：包装函数被错误内联或 traceback 丢帧；
- 行号缺失：调试信息未生成；
- GC 后 payload 错乱：panic 帧栈映射漏标。

## 参数

无随机路径。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_wrapped_error_panic/*.go
GODEBUG=clobberfree=1 /tmp/case
```
