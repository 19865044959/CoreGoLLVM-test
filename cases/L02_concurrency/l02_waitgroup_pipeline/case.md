---
id: l02_waitgroup_pipeline
dim: L02_concurrency
priority: P1
sources:
  - webrtc@v4.1.2: operations_test.go:13 16 个 operation × 100 轮 FIFO 执行
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 多写单读队列的完整性与收敛
tags: [L02, WaitGroup, pipeline, channel, pointer-payload, stage-close, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 pion/webrtc operations 队列与 grpc-go buffer 的多级传递：4 个 source goroutine 生成 320 个 `*item`，经 cap=8 的 channel 交给 3 个 transform goroutine，每个 transform 新建输出 `*item` 并写入 cap=6 的 channel，最后单 sink 聚合。每级用独立 WaitGroup 收敛，`Wait` 完成后才 close 下游 channel。transform 每 8 条、sink 每 32 条显式分配垃圾并 `runtime.GC()`，并在 GC 后同时校验输入/输出指针内容。

# 期望行为

1. sink 恰好收到 320 条消息，无重复、无丢失；
2. 每条 `transformVal(id) == (id*3+7)*2+1`，聚合校验和等于主 goroutine 独立计算值；
3. source 输入与 transform 输出指针跨 GC 后内容不变；
4. 所有 stage 在 close 下游前已排空，sink 正常退出，无 panic/死锁；
5. 最后一行 `ALL PASS l02_waitgroup_pipeline`。

# 失败判读

- `transform received corrupted input` / `sink value mismatch`：stage 间指针 payload 被破坏或串数据；
- `input/output corrupted after GC`：旧/新指针在 GC 后失效；
- `duplicate item at sink` / `missing item`：WaitGroup 与 close 时序错误导致重复或丢失；
- 超时：级间 channel 未正确关闭或容量死锁。

# 参数

无随机路径；`UAF_SEED` 由 runner 注入但本用例不使用。无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_waitgroup_pipeline/*.go
GODEBUG=clobberfree=1 /tmp/case
```
