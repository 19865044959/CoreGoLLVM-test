---
id: l07_pointer_array_slots
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - go-redis@v9.8.0: internal/pool/pool_test.go:307 借/还/删并发操作连接槽位，槽位不得悬挂
  - sonic@v1.13.2: issue_test/issue600_test.go:28 节点链 Unset 后剩余节点指针仍有效
tags: [L07, unsafe, pointer-array, atomic, pointer-arithmetic, nested-pointer]
requires: []
runtime: "<3s（TORTURE_ROUNDS 可放大）"
---

# 场景

注册表/对象池常用 `[]**node` 保存指向槽位的二级指针，配合 `unsafe.Add` 指针算术和 `atomic` 槽位替换。用例每轮构造 `[]*node`，把每个元素的地址放进 `[]**node` 后丢弃原切片，GC 后沿两级指针读取节点及其 `next` 链；再用单表达式 `unsafe.Add` 定位槽位，执行 atomic Store/Load、GC、CAS 换回，并校验槽位子切片。

# 期望行为

1. 原 `[]*node` 仅通过 `[]**node` 中的槽位指针可达时，GC 后每个节点及 `next` 链内容正确；
2. `unsafe.Add(&slots[0], k*sizeof(*node))` 等于 `&slots[k]`，解引用得到对应节点；
3. atomic 换入新节点后 GC，槽位仍指向新节点；被替换的旧节点由类型化指针保活并保持内容；
4. CAS 换回旧节点成功，GC 后节点及 `next` 有效；
5. `slots[lo:hi]` 子切片视图首尾槽位与原切片一致；
6. 丢弃所有引用再 GC，不崩溃、不误报；
7. 最后一行 `ALL PASS l07_pointer_array_slots`。

# 失败判读

- 槽位悬挂/内容错乱：`**node` 指针数组扫描位图错误，或内部指针未保活原 `[]*node` 底层数组；
- 节点 `next` 失效：二级指针解引用后的 `*node` 未被扫描；
- atomic 后读到旧节点：atomic 写缺少写屏障，或槽位未被 GC 当作根；
- 指针算术结果不等于 `&slots[k]`：元素宽度/步长计算错误；
- 仅在 stress/k8s 失败：GC=1 并发标记与 atomic 替换竞态。

# 参数

- `UAF_SEED`：决定每轮槽位数（8..15）与指针算术/atomic 目标槽位；
- `TORTURE_ROUNDS`：轮数，默认 64。每轮 3 次显式 GC，放大该值可做分钟级压测；
- 依赖 64 位指针宽度（linux/amd64、linux/arm64）。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_pointer_array_slots/*.go
UAF_SEED=1 TORTURE_ROUNDS=1000 GODEBUG=clobberfree=1 /tmp/case
```
