---
id: l03_defer_each_round
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: server/etcdserver/util_test.go:94 包装 fmt.Stringer，被包装者 panic 时用 defer/recover 回退到替代文本
  - grpc-go@v1.71.1: internal/cache/timeoutCache_test.go:51 TTL cache 每项到期回调与 Remove/Clear 的清理语义
tags: [L03, async-preempt, defer-per-round, recover, defer-record, pointer-capture, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

每轮调用 `//go:noinline` 的 `roundDefer`：函数内先登记带 recover 的外层 defer、带节点指针的清理 defer，非 panic 路径再登记第二个清理 defer，然后返回；每 211 轮在 defer 登记后 panic，由外层 defer recover。每 97 轮在 defer 尚未执行（栈上仍有 defer 记录）时调用显式 `runtime.GC()`。主循环另每 1000 轮在浅栈上显式 GC，并有一个 GC pump 并发执行 8 轮 GC。共 2048 轮，节点访问顺序由 `UAF_SEED` 决定。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_defer_each_round`；
3. defer 执行总次数等于非 panic 轮 2 次 + panic 轮 1 次的期望值；
4. defer 累加和等于按 LIFO 顺序计算的期望值（`p.v+2` 与 `p.w+3`）；
5. `recover` 次数等于 211 轮倍的 panic 轮数，panic 轮返回零值、非 panic 轮返回 `p.v+p.w`；
6. 主线程与 GC pump 的 GC 次数符合预期，全部节点字段在 GC 后完好；
7. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: per-round defer count` / `per-round defer LIFO sum`：defer 记录在 GC/panic 路径丢失或执行顺序错误；
- `FAIL: recover count`：recover 未命中或被优化掉；
- `FAIL: non-panic return values`：panic/recover 影响了正常返回值；
- `SIGSEGV`：defer 记录或闭包捕获的节点指针未被栈扫描。

## 参数

- `UAF_SEED`：决定每轮节点下标；失败信息带出种子；
- 无其他放大参数，P1 固定规模。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_defer_each_round/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
