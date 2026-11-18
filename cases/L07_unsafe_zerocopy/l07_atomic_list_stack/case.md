---
id: l07_atomic_list_stack
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - sonic@v1.13.2: ast/buffer_test.go:42 节点块链表 Push/Pop 重接 next 指针
  - sonic@v1.13.2: issue_test/issue600_test.go:28 节点链 Unset 后剩余节点指针仍有效
  - pgx@v5.7.4: pgxpool/pool_test.go:111 Acquire/Release 基本生命周期
tags: [L07, atomic, lock-free-list, CAS, liveness, write-barrier]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

`atomic.Pointer[T]` 头部的 Treiber 栈式链表：push 用 CAS 发布不可变节点，pop 用 CAS 摘除；节点含 string/[]byte/typed next，链上节点只经 atomic 头可达。每轮入栈后显式 GC 再遍历，弹出部分节点由类型化引用保活；最后做读者 goroutine 并发遍历、主线程 push/pop 并触发 GC 的压测。

## 期望行为

1. 入栈后节点只经 atomic 头可达，GC 后从头遍历数量、seq/tag/data 全部正确；
2. 弹出部分节点后，剩余链表数量正确，被弹出节点由类型化切片保活且 GC 后可读；
3. 再次入栈、GC、全量 drain，所有节点 payload 正确，头最终为 nil；
4. 并发阶段读者至少完成一次完整遍历，每个观察到的节点 payload 自洽；主线程 drain 数量精确，结束后头为 nil；
5. 多轮重复后无悬挂、无崩溃，末行 `ALL PASS l07_atomic_list_stack`。

## 失败判读

- GC 后遍历到错误 payload/SIGSEGV：atomic 头未被当作 GC 根，或节点 next 链被提前回收；
- 弹出节点失效：被替换下来的节点仅由类型化引用保活时未被栈位图标记；
- CAS 后计数不符/死循环：CompareAndSwap 语义或 next 发布顺序错误；
- 并发阶段读者报错：节点发布缺少写屏障，或 next 指针被并发改写；
- 仅 stress/k8s 失败：GOGC=1 并发标记与 CAS 竞争；
- 超时：TORTURE_ROUNDS 过大或调度饥饿。

## 参数

- `UAF_SEED`：决定每轮节点数、payload 内容与并发阶段 seed，失败信息带种子；
- `TORTURE_ROUNDS`：顺序轮数，默认 32；放大后同样只执行一次并发阶段，可做分钟级压测。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_atomic_list_stack/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=5000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
