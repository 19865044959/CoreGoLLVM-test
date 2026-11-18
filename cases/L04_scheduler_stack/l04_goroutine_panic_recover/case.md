---
id: l04_goroutine_panic_recover
dim: L04_scheduler_stack
priority: P0
sources:
  - grpc-go@v1.71.1: mem/buffers_test.go:128 二次 Free 必须 panic，用错误路径快速失败
  - pion/webrtc@v4.1.2: pkg/media/h264reader/h264reader_test.go:65 空输入/仅有起始码时返回 EOF 而非 panic
tags: [L04, panic, recover, defer, stack-unwind, pointer, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 gRPC buffer 非法时序 panic 与 WebRTC 解析器边界保护：默认 24 个 goroutine，每个构建 4 节点私有链，沿 `panicChain` 递归 5 层（每层 64 个指针槽），在叶子按 id 选择 panic 值——自定义 `panicPayload`（含 `*node` 与校验和）、字符串或 `error`。各 goroutine 在自身 defer 中 `recover`，校验 panic 类型、payload 中的指针身份与链内容；recover 后发出 ready 并阻塞，主协程显式 GC 两次后放行，再次校验链并汇总。

# 期望行为

1. 每个 goroutine 的 `recover()` 都返回非 nil，且类型与 id 对应的分支一致；
2. `panicPayload` 中的 `ptr` 与 `chain[(4+id)&3]` 指针身份相同、值一致，`sum` 等于链校验和；
3. 字符串/error panic 的值与 id 精确匹配；
4. 5 层栈展开后，链内容仍完整；
5. recover 后阻塞点 GC 不破坏栈上链指针；
6. 所有 worker 各返回一次结果，总和等于独立公式值；
7. 最后一行输出 `ALL PASS l04_goroutine_panic_recover`。

# 失败判读

- `recover/payload/chain broken`：panic 值在展开中丢失、指针被破坏，或 recover 未命中；
- `sum=... want=...`：recover 后链数据损坏；
- `missing result` 或进程直接崩溃：存在未 recover 的 panic，检查 panic 分支；
- 仅 efence/clobberfree 下失败：panic payload 或展开栈上的指针扫描问题。

# 参数

- `UAF_SEED`：链基值种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_goroutine_panic_recover/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 /tmp/case
```
