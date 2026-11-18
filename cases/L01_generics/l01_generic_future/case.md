---
id: l01_generic_future
dim: L01_generics
priority: P1
sources:
  - webrtc@v4.1.2: gathering_complete_promise_example_test.go:16 ExampleGatheringCompletePromise 返回在 ICE gathering 完成时关闭的 channel，供调用方等待
  - tailscale@v1.80.2: util/singleflight/singleflight_test.go:26 TestDo 多个调用者共享同一份结果
  - tailscale@v1.80.2: util/singleflight/singleflight_test.go:53 TestDoDupSuppress 并发调用只执行一次并广播结果
tags: [L01, generics, future, promise, once, broadcast, map-chain, all, error-propagation, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型 Future/Promise：`future[T]{done chan struct{}, once sync.Once, res}`，首次 resolve/reject 生效并通过 `close(done)` 广播；`await` 可被多个 goroutine 安全调用，`awaitOr` 支持取消通道；`mapFuture[A,B]` 与 `allFutures[T]` 用 goroutine 组合结果并按参数序收集错误。对应 pion/webrtc `GatheringCompletePromise` 的完成通知与 tailscale singleflight 的共享结果语义。

UAF_SEED 决定 All 中 future 数量（24–39）。用例覆盖：先 resolve 后 GC 再 await、先 GC 后 resolve、重复 resolve/reject 首次生效、错误指针身份、awaitOr 取消与完成、4 个等待者广播同一指针、Map 两段链与错误短路、Map fn 自身报错、All 按索引保序且错误传播、空 All 立即完成。显式 GC 放在 resolve 与 await 之间，验证通道关闭同步下的结果可见性与指针保活。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_future`；
2. 先 resolve 后显式 GC，await 仍返回原指针且 payload 内容正确；先 GC 后 resolve 同样成立；
3. 第二次 resolve 与 reject 不覆盖首次结果；reject 的 error 指针身份保持；
4. awaitOr 在 cancel 关闭且未完成时返回 ok=false，完成后用 nil cancel 返回 ok=true；
5. 4 个并发等待者都拿到同一 payload 指针；
6. Map 链 int64→string→int 结果正确；上游拒绝时下游 fn 执行次数为 0，错误原样传播；
7. All 的结果顺序严格按输入索引，与 goroutine 完成顺序无关，指针身份正确；任一拒绝使 All 返回该错误；空输入返回长度 0；
8. 所有 goroutine 正常结束，无死锁；clobberfree/GOGC=1 下结果一致。

# 失败判读

- `first completion not retained`：Promise 未用 once，后写覆盖了首次结果；
- `broadcast awaiter mismatch` / `all order/pointer mismatch`：共享结果被复制或 All 按完成顺序收集；
- `awaitOr` 相关：select 分支或 nil/closed channel 语义错误；
- `map fn executed after rejection` / 错误身份不符：Map 未短路或错误被包装；
- `resolved payload corrupted after GC`：`res` 中的指针未被 future 保活，偏 L18 写屏障；
- `fatal error: all goroutines are asleep - deadlock!`：done 未关闭或 All 等待顺序成环。

# 参数

- `UAF_SEED`：决定 All 的 future 数量与 payload 内容；失败输出带种子；
- 无 TORTURE 参数（P1 固定组合，单次 <10ms）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_future/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
