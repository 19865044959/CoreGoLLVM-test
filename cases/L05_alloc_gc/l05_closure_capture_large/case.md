---
id: l05_closure_capture_large
dim: L05_alloc_gc
priority: P1
sources:
  - fasthttp@v1.59.0: server_test.go:676 的 TestServerResponseBodyStream：流式响应闭包捕获 Response 后分块写出
  - fasthttp@v1.59.0: server_test.go:2264 的 TestServeConnKeepRequestAndResponseUntilResetUserValues：池化 ctx 在 Reset 前仍持有完整 user value
  - badger@v4.5.0: stream_writer_test.go:373 的 TestStreamWriterCancel：Prepare/Write 后 Cancel 的清理回调生命周期
tags: [L05, closure, escape-analysis, large-object, captured-slice, gc]
requires: []
runtime: "<10s"
---

# 场景

创建 20 个 256KB~320KB 的 slab 和一个 4MB slab，每个 slab 只由闭包暴露：
两个求和闭包共享同一 slab，另有 mutate/peek 闭包。创建者不保留底层指针；
显式 GC + 垃圾分配后调用闭包，验证捕获的大对象存活、两个闭包对同一 slab
的修改互相可见；释放一半闭包后剩余闭包仍有效。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_closure_capture_large`；
2. 创建后 GC 前，两个共享闭包的校验和均等于种子推导值；
3. mutate 闭包翻转一个字节后，两个求和闭包都观察到新校验和，peek 返回新字节；
4. 释放一半闭包并 GC 后，保留闭包仍返回正确校验和；
5. 仅保留 4MB 闭包并再 GC 时，捕获的 4MB 对象仍完整；
6. 全程无任何 slab 被提前回收或跨闭包串数据。

## 失败判读

- `sumA/sumB before mutate`：创建阶段捕获对象内容即错误；
- `did not observe shared mutation`：两个闭包未捕获同一对象（逃逸/登记错误）；
- `corrupted after dropping half closures`：闭包环境被 GC 提前回收；
- `4MB captured slab corrupted`：大对象仅由闭包持有时未被保活。

## 参数

- `UAF_SEED`：决定 slab 填充模式、修改位置与校验和；失败输出带种子；
- 无其他 TORTURE 参数；20 × 约 288KB + 1 × 4MB。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_closure_capture_large/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
