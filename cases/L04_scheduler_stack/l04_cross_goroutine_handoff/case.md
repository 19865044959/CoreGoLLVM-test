---
id: l04_cross_goroutine_handoff
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 10 个 writer 与 1 个 reader 并发队列交接，读到的集合必须完整且不重复
  - pion/webrtc@v4.1.2: datachannel_go_test.go:68 512 条消息单 channel 保序；处理函数在流中途被替换仍不丢消息
  - tailscale@v1.80.2: derp/derp_test.go:863 forwarder 映射热更新与 5000 个包并发转发
tags: [L04, channel, handoff, ownership, producer-consumer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC 消息队列与 WebRTC DataChannel 的指针所有权交接：goroutine A 每轮构建一条 8 节点链，通过 `chAB` 交给 goroutine B；主协程在 A 发出 sent、B 尚未取走时 GC 一次（指针仅存活于 channel 缓冲区），随后放行 B；B 取走后停在 gate 上，主协程再 GC 一次（指针仅存活于 B 的栈）。B 校验后原地给所有节点加 1000 并通过 `chBA` 交还，A 收回校验并保存。共 8 轮，所有轮次结束后主协程再 GC，逐轮复验 A 保存的链。

# 期望行为

1. A 收到的每条链长度均为 8，相邻节点差值仍为 13；
2. A 收到的链校验和等于 `8*base + 28*13 + 8000`；
3. B 在 GC 后看到的原始链校验和等于 `8*base+364`；
4. 指针在 channel 缓冲区与 B 栈上各经历一次 GC 后内容不损坏；
5. 最终 GC 后主协程持有的 8 条链全部完整；
6. 最后一行输出 `ALL PASS l04_cross_goroutine_handoff`。

# 失败判读

- `A observed corrupted chain`：交还路径上链被破坏或所有权被并发访问；
- `B observed corrupted chain after GC`：指针只在 B 栈上时 GC 扫描出错；
- `chain shape broken` / `sum=...`：节点丢失、重复释放或值被覆盖；
- 超时：sent/permit/gate/done 协调顺序错误或 channel 容量不足；
- 仅 k8s 下失败：指针在 channel 与栈之间交接时的调度竞争问题。

# 参数

- `UAF_SEED`：每轮链基值种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_cross_goroutine_handoff/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=efence=1 /tmp/case
```
