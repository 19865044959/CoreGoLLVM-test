---
id: l19_timer_panic_recover
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:470 lame-duck 收尾用 time.AfterFunc 看门狗回调，回调必须可靠执行
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲 timer 回调触发状态迁移，回调路径不得 panic 泄漏
  - nats-server@v2.11.0: server/raft_test.go:1677 用 recover 断言回调/停止路径不得 panic
tags: [L19, afterfunc, timer-callback, panic, recover, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟定时器回调内部发生 panic 的防护路径：64 个 `time.AfterFunc` 回调在显式 GC
后 panic 一个含指针链的 payload，回调内 defer/recover 必须拿到原对象并校验；
另有 64 个正常 timer 回调并发执行。最后再触发一次 panic/recover，确认恢复后
runtime 定时器路径仍可正常工作。

## 期望行为

1. 64 个 panic 回调全部被 recover 接住（`recovered=64`），无一遗漏；
2. recover 到的 payload 类型、id、base 与 3 节点指针链在 GC 后完好；
3. 64 个正常 timer 回调全部执行，校验和正确，不受并发 panic 影响；
4. 恢复后再次挂入的 timer 回调仍能 panic/recover 并发出完成信号；
5. 主协程持有的 payload 链在最终 GC 后不变；
6. 最后一行输出 `ALL PASS l19_timer_panic_recover`，失败退出码为 1。

## 失败判读

- 进程直接崩溃/退出码 2：回调内 recover 未执行，panic 逃逸到 runtime；
- `recovered=... want=64`：部分 timer 回调未运行或 recover 丢失；
- `payloads corrupted`：panic 展开/恢复路径上的栈或 timer 闭包指针保活错误；
- `sum` 不符：正常回调被 panic 路径影响；
- 超时 10s：panic 展开死锁或回调 goroutine 泄漏。

## 参数

- `UAF_SEED`：随机路径种子，参与 payload 基值计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_panic_recover/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
