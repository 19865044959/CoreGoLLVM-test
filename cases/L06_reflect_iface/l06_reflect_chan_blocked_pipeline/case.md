---
id: l06_reflect_chan_blocked_pipeline
dim: L06_reflect_iface
priority: P2
sources:
  - pgx@v5.7.4: pgconn/ctxwatch/context_watcher_test.go:31 cancel 回调向 channel 发送，接收方阻塞等待
  - caddy@v2.10.0: replacer_test.go:245 ReplacerFunc 函数值 provider 链被逐个调用
  - yaegi@v0.16.1: interp/interp_eval_test.go:653 解释代码经函数值回调宿主 func
  - echo@v4.13.4: echo_test.go:1226 reflect.Value.MethodByName(...).Call 批量注册反射回调
tags: [L06, reflect, chan, blocked-goroutine, MakeChan, func-value, MakeFunc, Select, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

动态装配层会用 `reflect.MakeChan/MakeFunc` 创建类型在编译期未知的函数通道，生产者/消费者可能
在 `reflect.Value.Send/Recv` 上阻塞，此时 GC 会扫描阻塞 goroutine 的栈与通道缓冲。本用例覆盖
消费者阻塞在 reflect Recv、生产者阻塞在 reflect Send、阻塞接收被 Close 唤醒、`chan any` 中
函数与指针混合、方向化函数通道、`reflect.Select` 取函数值，以及闭包捕获的 `*node` 在 GC 后
继续可调用。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_chan_blocked_pipeline`；
2. 消费者阻塞在 reflect Recv 时 GC，随后发送的 MakeFunc 函数值与捕获的 `*node` 仍正确；
3. 生产者阻塞在无缓冲 reflect Send 时 GC，接收到的函数值可调用且结果正确；
4. 阻塞接收方在 Close 后被唤醒并得到 `ok=false`；
5. `chan any` 中的 func、`*node`、string、nil 接口元素跨 GC 保持动态类型；方向化
   `chan<- func`/`<-chan func` 转换后函数值可传递并调用；
6. `reflect.Select` 取出的函数值在 GC 后可调用，闭包捕获对象未被回收；
7. 默认 4 轮，`TORTURE_ROUNDS` 可放大；所有 profile 输出一致。

# 失败判读

- `blocked reflect recv/send ... after GC`：阻塞在反射通道操作的 goroutine 栈未被扫描，或通道
  缓冲区中的函数值/指针未保活；
- `chan any ... after GC`：接口元素数据字未按动态类型扫描；
- `directional func chan after GC`：方向通道转换后元素类型元数据错误；
- `reflect.Select func value after GC`：Select 接收路径或闭包环境保活失败；
- 超时/死锁：阻塞的发送/接收未被 GC 后的操作解除。

# 参数

- `UAF_SEED`：每轮基值与标签值来源；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 4，上限 2000）。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_chan_blocked_pipeline/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=100 UAF_SEED=7 /tmp/case
```
