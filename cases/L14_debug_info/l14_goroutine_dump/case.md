---
id: l14_goroutine_dump
dim: L14_debug_info
priority: P1
sources:
  - docs/DIMENSIONS.md: L14 调试信息（goroutine 栈展开）
tags: [L14, goroutine-dump, stack-unwind, blocked]
requires: []
runtime: "<3s"
---

# 场景

8 个 goroutine 阻塞在 channel 接收上，`runtime.Stack(buf,true)` 打印所有 goroutine 栈；检查其中出现阻塞函数符号、`goroutine` 头、`main.main` 及 `chan receive` 状态。随后唤醒并校验每个 goroutine 持有的指针在 GC 后仍有效。

# 期望行为

1. 全量 dump 包含 `blockedWorker`、`goroutine `、`main.main`、`chan receive`；
2. 唤醒后 8 个 worker 全部看到 `v=7,name=blocked`（errs==0）；
3. 最后一行 `ALL PASS l14_goroutine_dump`。

# 失败判读

- dump 缺符号/状态：goroutine 栈展开或状态输出错误；
- 唤醒后指针损坏：阻塞栈帧的 stack map 漏标。

# 参数

固定 8 个 worker。

# 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_dump/*.go
GODEBUG=clobberfree=1 /tmp/case
```
