---
id: l14_goroutine_label_dump
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_goroutine_label_dump/main.go:58 - 阻塞 worker glBlocker
  - cases/L14_debug_info/l14_goroutine_label_dump/main.go:64 - pprof.Do 启动带标签 goroutine
  - cases/L14_debug_info/l14_goroutine_label_dump/main.go:124 - runtime.Stack 全量 dump 断言
  - cases/L14_debug_info/l14_goroutine_label_dump/main.go:131 - pprof goroutine profile 标签断言
  - docs/DIMENSIONS.md:22 - L14 调试信息（goroutine dump）
tags: [L14, goroutine-dump, pprof-labels, runtime.Stack, created-by, gc]
requires: []
runtime: "<3s"
---

# 场景

4 个 goroutine 阻塞在 channel 接收上，其中 3 个用 `pprof.Do` + `pprof.Labels` 打上不同的 `request`/`worker` 标签，1 个不打标签。用 `runtime.Stack(buf,true)` 抓全量 dump，再用 `pprof.Lookup("goroutine").WriteTo(...,1)` 抓带标签的 goroutine profile。对应线上按请求标签抓协程栈、定位某类请求挂死 goroutine 的排障路径。

## 期望行为

1. `runtime.Stack` 全量 dump 含 `main.glBlocker(`、`chan receive`、`created by main.glLaunch`、goroutine 头与 `main.go:<行号>`；
2. goroutine profile 中恰好出现 3 个 `# labels:` 行，且逐字匹配 `{"request":"req-N", "worker":"wN"}`（键按字典序、逗号后一个空格）；
3. profile 含全部阻塞 worker 符号与 `main.go:<行号>`；
4. 未打标签的第 4 个 goroutine 不产生 `"worker":"w3"`，三组标签互不串扰且各出现一次；
5. 释放 channel 后 4 个 worker 都执行并递增各自 payload.id；
6. 显式 GC 后 payload 有效，最后一行 `ALL PASS l14_goroutine_label_dump`。

## 失败判读

- 标签数量不是 3：标签继承/丢失，或未打标签 goroutine 被误标；
- 标签字符串键序或空格不符：标签序列化格式错误；
- profile 缺少 worker 符号或行号：goroutine profile 调试信息缺失；
- worker 未全部运行：标签测试的阻塞/释放逻辑错误。

## 参数

无随机路径。抓 profile 前轮询全量 dump 直到所有 worker 进入 `chan receive`，避免采样到启动中的栈；断言只检查是否出现与计数，不依赖 goroutine id 或栈顺序。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_goroutine_label_dump/*.go
GODEBUG=clobberfree=1 /tmp/case
```
