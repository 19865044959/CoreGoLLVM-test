---
id: l07_unsafe_global_atomic
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 解码输入 buffer 由外部引用跨 GC 存活（CopyString 生命周期）
  - go-redis@v9.8.0: internal/pool/pool_test.go:71 池槽位全局共享，对象在借还之间必须不被回收
tags: [L07, unsafe, atomic, global, liveness]
requires: []
runtime: "<10s"
---

# 场景

全局 `unsafe.Pointer` 与 `sync/atomic` 的指针操作（Store/Load/Swap/CAS）是零拷贝解析器、对象池和配置热更新的常见手法。如果把指针错误地保存为 `uintptr`，或 atomic 槽位未被 GC 当作根，对象会在 GC 后被回收。用例把带 string/slice/`[]*node` 的 payload 放入普通全局槽位和 atomic 槽位，跨多轮 GC、CAS、Swap 后逐字段校验，并在清空槽位后用类型化局部引用继续保活。

# 期望行为

1. `atomic.LoadPointer` 与普通全局 `unsafe.Pointer` 读出的 payload 在 GC 后 tag/data/items/pad 全部正确；
2. CAS 成功替换、过期 CAS 被拒绝，被替换下来的旧 payload 经类型化局部引用在 GC 后仍可读；
3. `SwapPointer` 返回旧 payload，且新/旧 payload 均跨 GC 有效；
4. 反复改写普通全局槽位并 GC，内容始终正确；
5. 清空两个槽位后，类型化引用保活的三份 payload 仍然可读；
6. 最后一行 `ALL PASS l07_unsafe_global_atomic`。

# 失败判读

- 内容错乱/崩溃：全局或 atomic 槽位未被扫描（写屏障/栈映射缺失），或指针被存成 `uintptr`；
- 仅 stress/k8s 失败：GC=1 下并发标记与 atomic 写竞争，槽位读取到半更新状态（原子性缺失）；
- 旧 payload 失效：被替换对象仅由 local 类型化指针保活时未被栈位图标记。

# 参数

- `UAF_SEED`：决定 payload 内节点数 `4+seed%3`，失败信息带种子；
- 无 TORTURE 参数（固定 4 轮全局轮换）。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_unsafe_global_atomic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
