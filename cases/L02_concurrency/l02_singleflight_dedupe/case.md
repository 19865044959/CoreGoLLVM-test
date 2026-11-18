---
id: l02_singleflight_dedupe
dim: L02_concurrency
priority: P0
sources:
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 10000 goroutine 并发首次 Get，初始化函数只执行一次且所有调用得到同一结果
  - etcd@v3.5.0: pkg/wait/wait_test.go:23 单次事件注册/触发；:64 重复触发被抑制
  - nats-server@v2.11.0: server/sublist_test.go:1127 重复 Insert 不重复通知，合并为一次兴趣事件
tags: [L02, singleflight, dedupe, shared-result, error-not-cached, waitgroup, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

用标准库实现 singleflight 类去重：`flight.Do(key, fn)` 对同一 key 的并发调用只执行一次 `fn`，其余调用等待并共享同一 `*payload` 结果；`Do` 返回 `shared` 标志。

三个阶段，全部用 gate channel 与等待者计数协调，不使用 sleep：

1. `alpha`：8 个并发调用，leader 在 `fn` 中阻塞；主 goroutine 等到 7 个 follower 登记后放行，断言仅 1 次计算、恰好 1 个调用 `shared=false`、8 个结果指向同一对象；
2. 不同 key：`k0/k1/k2` 各 3 个并发调用，3 个 leader 全部进入 `fn` 且每 key 有 2 个 follower 后统一放行，断言每 key 只计算一次；
3. 错误：4 个并发调用共享 `exec failed` 错误但错误不缓存；随后同一 key 的顺序调用必须重新执行并成功返回。

计算过程中显式分配垃圾并 `runtime.GC()`，校验共享结果指针跨 GC 有效。

# 期望行为

1. `alpha` 计算次数为 1，等待者去重计数为 7，`shared=false` 的调用恰好 1 个；
2. 8 个 `alpha` 结果指针完全相同且 payload 内容正确；
3. 3 个不同 key 各计算 1 次，共 3 次，6 个 follower 全部合并；
4. 错误阶段 4 个调用都收到同一 `exec failed`，计算仅 1 次；
5. 错误完成后同 key 再调用会重新计算（累计 2 次），返回新结果且 `shared=false`；
6. 所有结果在显式 GC 后内容不变；
7. 最后一行输出 `ALL PASS l02_singleflight_dedupe`，退出码 0。

# 失败判读

- `alpha computes=N want=1`：并发去重失效，map 登记或加锁窗口有竞态；
- `alpha leaders=N want=1`：`shared` 标志错误；
- `not all ... followers enrolled`：等待者在 gate 放行前未完成登记，说明 `Do` 的 follower 计数或调度同步错误；
- `error result ... want exec failed`：错误共享路径未传播；
- `err computes after retry=N want=2`：错误被错误缓存；
- 结果指针不一致/GC 后内容损坏：共享对象发布或指针存活问题。

# 参数

- `UAF_SEED`：决定 payload 数值；
- 无 `TORTURE_*`；迭代固定。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_singleflight_dedupe/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
