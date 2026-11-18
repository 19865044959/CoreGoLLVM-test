---
id: l12_init_defer_panic_nil
dim: L12_init_link
priority: P0
sources:
  - go@1.24.1: src/runtime/panic.go:712 panic(nil) 自 Go 1.21 起 recover 返回 *runtime.PanicNilError
  - go@1.24.1: src/runtime/panic.go:724 PanicNilError.Error() 文本 "panic called with nil argument"
  - fiber@v2.52.6: router_test.go:25 init() 中读取 fixture 失败后 :28 panic(err)
  - fiber@v2.52.6: middleware/compress/compress_test.go:17 init() 失败路径 panic(err)
tags: [L12, init, defer, LIFO, panic, panic-nil, recover, nested-panic, cross-file-init, gc]
requires: []
runtime: "<3s"
---

# 场景

三个文件各有一个 `init`，只断言每个 init 块内部的规范保证顺序，不断言跨文件顺序。init A 在 defer 中 recover `panic(nil)`；init B 在已 panic 的展开过程中由 deferred 函数再次 panic，早注册的 defer 恢复第二次 panic；init C 通过辅助函数 recover，并验证在非 defer 位置直接调用 `recover()` 返回 nil。

## 期望行为

1. trace 总长 11，三个 init 块各出现一次（作为连续子序列）；
2. init A 块为 `a:enter,a:before,a:recover,a:defer`（defer LIFO）；
3. A 的 recover 值非 nil，类型为 `*runtime.PanicNilError`，错误文本为 `panic called with nil argument`；
4. init B 块为 `b:enter,b:before,b:defer-panic,b:recover-second`：deferred panic 覆盖原始 `b-first`，由后执行的 recover 拿到字符串 `b-second`；
5. init C 块为 `c:enter,c:after-helper,c:defer`；`cDirect==nil`（非 defer 位置的 recover），辅助函数 recover 到 `c-helper`；
6. 8 goroutine 并发只读 trace 并穿插显式 GC 时全部断言成立；
7. GC 后 trace、recovered 值、panic 类型与恢复状态不变；
8. 最后一行 `ALL PASS l12_init_defer_panic_nil`。

## 失败判读

- 某块缺失/顺序不符：init 内 defer LIFO、嵌套 panic 覆盖或同文件 init 语义错误；
- `panic(nil)` 恢复为 nil 或类型不是 `*runtime.PanicNilError`：panicnil GODEBUG 默认或运行时 panic 转换错误（若环境显式设置 `GODEBUG=panicnil=1`，本项预期失败）；
- init B 只恢复 `b-first` 或进程终止：deferred panic 的运行时展开/恢复语义错误；
- `cDirect` 非 nil：recover 调用位置规则错误；
- GC 后 trace 变化：初始化期全局切片/字符串未被扫描；
- 超时：init 内死锁（本用例无阻塞点）。

## 参数

- `UAF_SEED`：决定并发 worker 的起点与显式 GC 轮数（2..4）；断言与种子无关，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_defer_panic_nil/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
