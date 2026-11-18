---
id: l11_escape_channel
dim: L11_compiler_opts
priority: P2
sources:
  - fasthttp@v1.59.0: server_test.go:944 并发处理上限用带缓冲 chan 信号量，超限立即拒绝
  - echo@v4.13.4: middleware/timeout_test.go:204 timer 与 handler 经 channel 竞争
  - gorm@v1.30.0: tests/prepared_stmt_test.go:153 单连接下 100 goroutine 经信号量排队
tags: [escape-analysis, channel, goroutine, pipeline, interface-boxing, closure, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS=50 时 200 轮）"
---

# 场景

模拟请求对象在 worker/超时/流水线 channel 间传递：带缓冲 channel 发送本函数内分配的对象指针，发送后在对象仍在 channel 里时显式 GC；接口 channel 交替发送指针与值装箱；闭包 channel 发送捕获循环变量的函数；map channel 发送后继续写入；关闭的缓冲 channel 中残留值经 GC 后仍可顺序读出；非缓冲 channel 的生产者在主 goroutine GC 时阻塞在发送点；两段 goroutine 流水线在传递中修改对象并周期性 GC；`select` 单就绪分支接收。每轮保留部分节点，最终 GC 后复核。

# 期望行为

1. 缓冲 channel 满时 `len/cap` 均为 n，GC 后逐节点 `id/tag/data` 完整且顺序不变；
2. 接口 channel 指针/值装箱数量各半，动态类型与内容正确；
3. 闭包 channel 求和等于闭式期望；
4. map 经 channel 传递后跨 GC 可读可写；
5. 已关闭缓冲 channel 中 n 个值全部按序读出，无丢失；
6. 非缓冲生产者在接收方 GC 后继续完成，全部节点 ID 有序；
7. 流水线节点 ID 加 100、首字节异或固定值，GC 插入不破坏变换；
8. `select` 在仅一个分支就绪时稳定取得该分支；
9. 每轮结束后保留节点在最终 GC 后 `tag`、数据长度与 ID 仍有效；
10. 最后一行输出 `ALL PASS l11_escape_channel`。

# 失败判读

- `buffered channel node corrupted after GC`：经 channel 逃逸的对象未被正确标记/保留；
- `interface channel ... corrupted`：接口装箱数据丢失或动态类型错误；
- `closure channel sum mismatch`：闭包环境被回收或捕获错误；
- `pipeline ... mismatch`：流水线中间对象被复用/破坏；
- `closed buffered channel ...`：关闭后缓冲值丢失或顺序改变；
- 超时：goroutine 死锁或未退出；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮节点数 `48..63`、数据 salt 与基础长度，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（4 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_escape_channel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_escape_channel/*.go
UAF_SEED=1 /tmp/case_n
```
