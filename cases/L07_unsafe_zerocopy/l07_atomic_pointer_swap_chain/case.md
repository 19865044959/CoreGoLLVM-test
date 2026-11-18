---
id: l07_atomic_pointer_swap_chain
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 Get/Put/Remove 并发操作连接槽位，槽位不得悬挂
  - fasthttp@v1.59.0: http_test.go:456 TestResponseSwapBodyConcurrent 池化对象间 Swap 交换缓冲区所有权
  - go-redis@v9.8.0: internal_test.go:86 TestRingSetAddrsAndRebalanceRace 分片表并发替换指针
tags: [L07, atomic, pointer-swap, CAS, concurrent, write-barrier]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

`atomic.Pointer[T]` 的 Store/Load/Swap/CompareAndSwap 是配置热更新、对象池槽位和连接表替换指针的常用手法。用例在多轮 Store→GC→Load、Swap 换下旧对象、CAS 成功/过期失败、槽位链式所有权转移中逐字段校验 payload（string/[]byte/`*pnode`/`[]*pnode`），并用写者/读者 goroutine 并发交替 Store 两个不可变 payload、读者周期性触发 GC，验证原子指针在并发标记期间的可见性与自洽性。

# 期望行为

1. Store 后 GC，Load 返回同一指针且 payload 各字段正确；
2. Swap 返回旧指针、装入新指针；被换下的旧 payload 由类型化局部引用在 GC 后仍可读；
3. CAS 成功替换、过期 CAS 返回 false；失败方与成功方对象在 GC 后均有效；
4. 清空槽位后，类型化引用保活的 payload 仍可读；
5. 三个槽位通过 Swap 完成 0→1→2 的所有权转移，GC 后槽位指针与全部对象正确；
6. 并发写者 512+ 次交替 Store A/B，读者两倍次数 Load 并校验 gen/name/data/next 不变量，周期性 GC 无失配；
7. 并发结束后最终槽位指向 A 或 B，清空槽位并 GC 后两个 payload 仍完整；
8. 最后一行 `ALL PASS l07_atomic_pointer_swap_chain`。

# 失败判读

- Load 返回 nil/错误对象：atomic 槽位未被 GC 当作根，或 Swap/CAS 语义实现错误；
- 被换下对象字段错乱/efence 崩溃：类型化局部引用未被栈位图标记，或老对象被提前回收；
- 并发校验出现失配：atomic 写缺少写屏障/原子性，或旧 payload 仅由 atomic 槽位保活时被回收；
- 仅 stress/k8s 失败：GOGC=1 并发标记与 atomic 写入竞争；
- 超时：并发迭代因 GC 饥饿卡住。

# 参数

- `UAF_SEED`：决定 payload 代数基数与并发迭代次数，失败信息带种子；
- `TORTURE_ROUNDS`：顺序轮数，默认 20；放大时同时增大并发迭代次数，可做分钟级压测。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_atomic_pointer_swap_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=200 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
