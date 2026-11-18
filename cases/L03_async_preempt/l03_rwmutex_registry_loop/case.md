---
id: l03_rwmutex_registry_loop
dim: L03_async_preempt
priority: P1
sources:
  - caddy@v2.10.0: modules_test.go:22 全局模块注册表由 modulesMu 保护，GetModules 在锁内读取后返回模块信息
  - etcd@v3.5.0: client/pkg/types/set_test.go:27 线程安全 Set 与 unsafe Set 共用断言，Add/Contains/Values/Remove 覆盖并发读写
tags: [L03, async-preempt, RWMutex, read-mostly, registry, pointer-holder, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

RWMutex 保护的注册表有 128 个槽位：writer 每轮分配新的不可变节点并替换槽位；主 goroutine 和第二个 reader 在 RLock 内取节点指针，RUnlock 后跨显式 GC 解引用。主循环每 2048 次读 GC 一次，GC pump 并发执行，writer 侧也有周期 GC。

# 期望行为

1. 主 reader 的读校验和与按槽位公式计算的参考值一致；
2. 从 RLock 取出的节点在 RUnlock + GC 后仍可读，id/value/tag 不变；
3. writer 完成全部 20000 次替换；
4. 第二个 reader 完成 20000 次读取且无坏节点；
5. 主循环、writer 的显式 GC 次数符合固定公式；
6. 最终注册表所有槽位自洽；
7. GC pump 至少完成一轮，退出码 0，最后一行 `ALL PASS l03_rwmutex_registry_loop`。

# 失败判读

- `FAIL: pointer read under RLock survives unlock+GC`：解锁后局部指针未被正确标记；
- `FAIL: RWMutex read checksum`：读到的节点与槽位不匹配或值被覆盖；
- `FAIL: concurrent reader` / `final registry intact`：并发替换破坏注册表；
- 超时：RWMutex 写者饥饿或死锁。

# 参数

- `UAF_SEED`：决定主 reader 的槽位步长与起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_rwmutex_registry_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
