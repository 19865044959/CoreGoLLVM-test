---
id: l17_closure_capture_escape
dim: L17_stackmap
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37（100 个调度闭包，闭包内 defer + select）
  - fasthttp@v1.59.0: server_test.go:2914（TimeoutHandler 使用闭包 handler，select/time.After 收尾）
tags: [L17, stackmap, closure, closure-table, goroutine, per-iteration, nested-closure, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 5 轮）"
---

# 场景

闭包表与逃逸捕获：工厂为每个循环迭代创建独立 `*item`，分别被 slice 闭包与方法 map 闭包捕获；闭包在工厂返回并 GC 后逐个调用。另覆盖大 struct `bundle`（内嵌 `[4]*item`）按值捕获、两层嵌套闭包捕获同一 `*item`，以及在 goroutine 内调用闭包、主 goroutine GC 后从 channel 取回结果。最后再 GC 并重复调用全部闭包。

## 期望行为

1. slice 闭包返回 `v+(v+100)+len("cap")`，map 闭包返回 `2*v`，v 按每轮 base 与下标计算；
2. goroutine 内调用的闭包与 `kept` 中的指针在跨 GC 后结果一致；
3. `bundle` 闭包按值捕获的 4 个指针跨 GC 有效，返回值等于 bundle 和；
4. 嵌套闭包返回的 `root`/`root.next` 在两次 GC 后仍可读；
5. 工厂返回后、全部调用后各校验一次，无闭包捕获失效；
6. 最后一行 `ALL PASS l17_closure_capture_escape`，退出码 0。

## 失败判读

- `fns[i]`/`tbl fN` 结果错误：闭包捕获槽未按指针扫描，item 被回收；
- `goroutine closure` 失败：闭包在跨 goroutine 的栈槽漏标；
- `bundle closure` 失败：按值捕获的大 struct 内指针位图缺失；
- `nested closure corrupted`：内层闭包 capture 的指针失效；
- 仅 clobberfree 下失败：捕获对象被提前回收；
- 崩溃：闭包调用点栈映射错误。

## 参数

- `UAF_SEED`：决定每轮 base 与闭包数量（8–11）；
- 无 TORTURE 参数，5 轮固定。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_closure_capture_escape/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
