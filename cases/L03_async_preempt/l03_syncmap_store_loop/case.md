---
id: l03_syncmap_store_loop
dim: L03_async_preempt
priority: P2
sources:
  - gorm@v1.30.0: schema/schema_test.go:13 schema.Parse 以 sync.Map 作为解析缓存，反射解析期间缓存对象必须存活
  - gorm@v1.30.0: tests/prepared_stmt_test.go:153 MaxOpenConns(1) 下并发 PrepareStmt，语句缓存的去重与 Close 路径必须安全
tags: [L03, async-preempt, sync.Map, store-load-delete, Range, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

在 512 个固定字符串键上循环执行 sync.Map 的 Store/Load/Delete：每轮更新一个键、周期性删除并在后续轮次重新插入，值是指向不可变堆节点的指针。后台 Range goroutine 持续并发遍历，遍历顺序不参与判定；主循环每 64 轮显式 GC，另有 GC pump 并发。结束后按键号顺序和最终 Range 汇总双重校验。

# 期望行为

1. 每个 Store 后的 Load 都返回同一代节点，Delete 后立即不可见；
2. 后台 Range 只看到完整节点，不出现悬挂/半初始化值；
3. 最终按键号顺序检查的 present/gen/value 与操作记录一致；
4. 最终 Range 的条目数、值的和与顺序无关，且等于参考值；
5. 主循环显式 GC 次数符合 `(rounds-1)/64+1`；
6. GC pump 至少完成一轮；
7. 退出码 0，最后一行 `ALL PASS l03_syncmap_store_loop`。

# 失败判读

- `FAIL: sync.Map Store/Load/Delete results`：并发 GC 后值指针失效或 Map 状态不一致；
- `FAIL: per-key final state` / `final Range count/sum`：删除/重插语义或键值对应关系错误；
- `FAIL: Range saw only valid immutable values`：Range 回调拿到被回收/覆盖的节点；
- 超时：后台 Range 与主循环争用导致饥饿。

# 参数

- `UAF_SEED`：决定步长与起始键偏移，失败信息带出种子；
- `TORTURE_ROUNDS`：放大主循环轮数，默认 4096。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_syncmap_store_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=65536 UAF_SEED=2 /tmp/case
```
