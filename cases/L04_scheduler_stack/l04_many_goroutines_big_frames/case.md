---
id: l04_many_goroutines_big_frames
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/replacer_test.go:26 深占位符/嵌套展开放大到万级时触发 morestack 与栈复制
  - pion/webrtc@v4.1.2: peerconnection_renegotiation_test.go:970 500 条 track 逐个推进事件循环，单 goroutine 栈持续变化
  - tailscale@v1.80.2: net/art/table_test.go:578 1 万前缀建表/查找长循环中穿插 GC 与调度点
tags: [L04, morestack, stack-copy, big-frame, many-goroutines, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Caddy 深占位符展开与 WebRTC 多 track 事件循环中“大量协程同时撑大栈帧”的路径：默认 48 个 goroutine，每个先在栈上声明 `[3072]*node`（24KB）与 `[1536]*node`（12KB）两个指针槽数组，再递归调用 `leafFrame` 声明 `[2048]*node`（16KB）。每个槽位按 `(i*mul+id+seed%3)&3` 指向本 goroutine 私有 4 节点链，逐槽做指针身份校验。所有 goroutine 在 36KB 帧存活期间停在 channel 接收上，主协程分配垃圾并显式 `runtime.GC()` 两次；唤醒后再调用一次大帧函数并逐槽复验。

# 期望行为

1. 每个 goroutine 在 GC 前后两次校验中，big/aux/leaf 三组槽位均严格指向本协程链上的同一批节点（指针身份，不仅是数值）；
2. 每个 worker 的校验和等于主协程用同一索引公式独立计算的值；
3. 全部 worker 各返回一次结果，无缺失、无重复；
4. 总校验和等于所有 worker 期望值之和；
5. 在 clobberfree/efence/stress/k8s 下结果一致；
6. 最后一行输出 `ALL PASS l04_many_goroutines_big_frames`。

# 失败判读

- `big-frame slots corrupted across GC`：大栈帧的栈映射漏标指针，GC 后槽位被覆盖/清零；
- `sum=... want=...`：槽位索引或指针身份校验失败，可能栈复制时指针未修正；
- `missing result` 或超时：goroutine 未到达 ready/release 同步点，检查 channel 协调；
- 仅 k8s/stress 下失败：GOMAXPROCS=2 时栈复制与调度竞争暴露的扫描问题。

# 参数

- `UAF_SEED`：链基值与槽位折叠偏移种子（默认 1）；
- `TORTURE_WORKERS`：goroutine 数，默认 48，范围 8–128。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_many_goroutines_big_frames/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_WORKERS=96 GOGC=1 GOMAXPROCS=2 /tmp/case
```
