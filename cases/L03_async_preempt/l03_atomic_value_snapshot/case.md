---
id: l03_atomic_value_snapshot
dim: L03_async_preempt
priority: P1
sources:
  - tailscale@v1.80.2: wgengine/magicsock/magicsock_test.go:1296 ep.disco.Store(&endpointDisco{...}) 原子发布端点快照，读者无锁读取
  - fasthttp@v1.59.0: server_test.go:408 Server.Name 由 atomic.Value 缓存，请求热路径反复读取
tags: [L03, async-preempt, atomic.Value, store-load, immutable-snapshot, reader-loop, pointer-liveness, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

单写者 goroutine 依次构造不可变快照 `{ver,node,sum,tag,pad}` 并用 `atomic.Value.Store` 发布 12000 个版本，读者主 goroutine 循环 Load 24000 次，校验 `ver/node/sum/pad` 自洽且版本号不回退；每 256 次读在持有旧快照时分配垃圾并显式 `runtime.GC()`，GC 后继续解引用旧快照的 node。写者此时已发布新版本，旧快照可能成为垃圾。写者每 512 版也显式 GC。结束后等待写者完成并校验最终快照。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_atomic_value_snapshot`；
2. 所有 Load 到的快照字段自洽（无撕裂读），版本号单调不减；
3. 读者在显式 GC 后仍能安全解引用所持旧快照的 node，字段一致；
4. 最终快照版本为 12000，node/sum/pad 与版本公式一致；
5. 读者迭代 24000 次、显式 GC 94 次；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: atomic.Value snapshots consistent across GC`：旧快照中的指针未被读者栈正确保活，或被 GC 提前回收；
- `FAIL: final snapshot valid` / `writer published all versions`：写者未完成或 Store 丢失；
- `FAIL: version sequence monotonic`：读到了非法/回退版本；
- 超时：写者/读者握手异常（检查 done channel）。

## 参数

- `UAF_SEED`：决定快照内容偏移；失败信息带出种子；
- 其他 TORTURE_* 参数：本用例不消费。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_atomic_value_snapshot/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
