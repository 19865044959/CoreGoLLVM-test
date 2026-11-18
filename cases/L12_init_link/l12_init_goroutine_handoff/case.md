---
id: l12_init_goroutine_handoff
dim: L12_init_link
priority: P1
sources:
  - etcd: server/main_test.go:51 TestMain 中 :62 go etcdmain.Main(appArgs)，:63 用 notifier channel 与主流程同步
  - tailscale@v1.80.2: ssh/tailssh/tailssh_integration_test.go:57 TestMain 中 :74 启动日志转发 goroutine
  - grpc-go@v1.71.1: gcp/observability/observability_test.go:55 init() 注释说明引入包会在初始化期启动全局 goroutine
tags: [L12, init, goroutine, channel, happens-before, handoff, pointer-graph, gc]
requires: []
runtime: "<10s"
---

# 场景

包级变量初始化阶段构建 96 节点单向链表指针图（`graph`），随后 `init` 启动三个 goroutine，它们都阻塞在 `startGate` 上。main 在全部初始化完成后 `close(startGate)`，利用 channel 的 happens-before 保证 worker 看到 init 完成的标志与完整指针图。两个 worker 分别回传抽样链与总和，第三个回传 ready 标记；主 goroutine 期间并发只读指针图并穿插显式 GC。

## 期望行为

1. `buildCount==1`、节点数 96、head 与 `nodes[0]` 同一指针、链表 score 总和 13776；
2. init 启动的 worker 观测到 `initDone==true`，且该检查恰执行一次；
3. handoff 链长度 32（id 为 3 的倍数）、id 递增、每个元素与 `graph.nodes[id]` 同一指针；
4. worker 与 reader 计算的总和均为 13776，ready 标记为 1；
5. 8 个 main 侧 goroutine 并发读链表与 payload，同时主 goroutine 显式 GC，无错误；
6. `stopRead` 关闭后所有 init 启动的 goroutine 退出（`initWG.Wait` 返回）；
7. GC 后链表总和、head 身份、handoff 链指针身份与 payload 字节不变；
8. 最后一行 `ALL PASS l12_init_goroutine_handoff`。

## 失败判读

- `init-started worker observed incomplete init state`：startGate 的 happens-before 错误，worker 看到未完成初始化；
- 总和/id/身份错误：全局指针图被破坏或构造不完整；
- 死锁/超时：init 启动的 goroutine 未退出或 channel 配对错误；
- GC 后 payload 或身份变化：全局链表指针扫描错误；
- 并发阶段失败：只读图中出现隐藏写入或 worker 间可见性问题。

## 参数

- `UAF_SEED`：决定 main 侧并发采样起点与显式 GC 轮数（2..4）；断言与种子无关，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_goroutine_handoff/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
