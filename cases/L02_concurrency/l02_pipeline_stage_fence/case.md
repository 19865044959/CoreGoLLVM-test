---
id: l02_pipeline_stage_fence
dim: L02_concurrency
priority: P1
sources:
  - go-redis@v9.8.0: pipeline_test.go:74 逐级增大到 15 条命令的 pipeline，结果数与顺序必须一致
  - fasthttp@v1.59.0: server_test.go:38 POST 后多一个 CRLF 的 pipelined 请求仍按序响应，多余 CRLF 不吞下一请求
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 个 goroutine 调度、单消费者串行执行，执行序等于调度序
tags: [L02, pipeline, stage-fence, bounded-channel, multi-stage, backpressure-drain, checksum, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟五级数据处理流水线（source -> transform x4 -> sink）在中途触发故障栅栏的收尾路径：第 3 级只放行前 50 条，其余条目必须继续排空丢弃，避免上游被有界 channel 反压卡死；下游只能收到放行条目。对应 grpc 回调串行器、fasthttp pipelined 请求和 go-redis pipeline 中“关闭/截断后必须收尾”的模式。

## 期望行为

1. 进程退出码为 0，所有 `ck/ckSeed` 断言成立；
2. 最后一行输出 `ALL PASS l02_pipeline_stage_fence`；
3. 第 1/2/3 级输入计数均为 120（上游全部处理完），第 3 级放行 50、丢弃 70；
4. 第 4 级与 sink 只收到 50 条，无重复、无缺失，逐条载荷与变换公式一致；
5. GC 前后 sink 校验和不变；所有 stage channel 正常关闭，无 goroutine 泄漏/死锁；
6. 在 clobberfree / efence / k8s / single 等 profile 下结果一致。

## 失败判读

- 计数不符：某一级提前退出或 close 级联错误，检查 `stage N input=` 消息；
- `sink received duplicate/missing`：fence 领取位置与放行判断有竞态；
- `corrupted after GC`：payload 指针在跨 stage 传递中被 GC/写屏障问题破坏；
- 超时：第 3 级未排空剩余输入，导致上游写 channel 永久阻塞。

## 参数

- `UAF_SEED`：决定各级载荷数值；失败输出带种子；
- 固定 3 源 × 40 条、fence 50，无墙钟阈值。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_pipeline_stage_fence/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
