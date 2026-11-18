---
id: l12_init_handoff_pipeline
dim: L12_init_link
priority: P1
sources:
  - etcd@v3.5.0: server/main_test.go:60 notifier channel 与 :62 go etcdmain.Main(appArgs) 的 goroutine 交接
  - tailscale@v1.80.2: ssh/tailssh/tailssh_integration_test.go:74 TestMain 中启动日志转发 goroutine
  - grpc-go@v1.71.1: gcp/observability/observability_test.go:55 init 注释说明引入包会在初始化期启动全局 goroutine
tags: [L12, init, goroutine, channel, happens-before, handoff, pipeline, pointer-graph, gc]
requires: []
runtime: "<10s"
---

# 场景

包级变量初始化构建通道与指针图；`init` 启动三个串联流水线 worker 与一个 token 交接 worker，全部阻塞在 `startGate`。main `close(startGate)` 后依次发送 64 个 `*job`，用 channel close 建立 happens-before；各 stage 在原指针上原地变换（`*3+1`、`+2`），结果经有缓冲 channel 回流。验证 init 启动的 goroutine、顺序、指针身份、负载与退出。

## 期望行为

1. 4 个 init 启动的 worker 各启动一次并观测到 gate 已打开；
2. 结果恰 64 个，顺序与发送顺序一致，每个结果指针与发送的 `*job` 同一；
3. `v_final==(base+i)*3+3`（base=`UAF_SEED%7`），id 递增 0..63；
4. 每个 job 的 `note` 指针身份保持，tag 为 `job-%02d`，字节内容不变；
5. token 指针与包级 `tokenPtr` 同一，payload 为 `handoff`/`{1,2,3,4}`；
6. 主 goroutine 在流水线运行期间显式 GC；GC 后并发只读 job 图无错误；
7. 所有 worker 在 channel 关闭后退出（`WaitGroup` 返回），无超时/死锁；
8. GC 后 job 指针、payload、token 与总和不变；
9. 最后一行 `ALL PASS l12_init_handoff_pipeline`。

## 失败判读

- worker 启动/gate 计数不为 1：init 启动或 channel happens-before 错误；
- 结果指针身份错误：job 被复制而非指针传递；
- 顺序/数值错误：stage 变换或 channel 顺序语义错误；
- 死锁/超时：有缓冲容量不足或 worker 未退出；
- GC 后 payload 或 token 变化：全局指针图扫描错误；
- 并发阶段失败：只读 job 图存在隐藏写入或可见性问题。

## 参数

- `UAF_SEED`：决定 base 值（0..6）与 GC 扰动；断言按 base 计算，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_handoff_pipeline/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
