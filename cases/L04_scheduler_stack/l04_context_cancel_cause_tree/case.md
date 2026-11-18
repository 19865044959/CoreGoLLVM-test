---
id: l04_context_cancel_cause_tree
dim: L04_scheduler_stack
priority: P0
sources:
  - go1.24.1: src/context/x_test.go:634 "WithCancelCause: parent cause before child" 覆盖父子 cause 传播的优先关系
  - go1.24.1: src/context/afterfunc_test.go:107 TestCustomContextAfterFuncUnregisterCancel 覆盖 AfterFunc 在取消时的注销语义
tags: [L04, context, WithCancelCause, AfterFunc, cancel-tree, propagation, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 gRPC/Caddy 的请求 context 树在两层结构下的带因取消：root 下 2 个 child、各 2 个 grandchild，共 7 个节点；每个节点一个 goroutine 阻塞在 `ctx.Done()` 上并持 2 节点指针链，每个节点注册一个 `context.AfterFunc`；另在 c0g1 上注册一个回调并在取消前 `Stop`。

1. 全部 goroutine 就绪后显式 GC；
2. 用 errMid 取消 c0，并重复 cancel 两次以验证首个 cause 获胜；c0/c0g0/c0g1 必须退出且 cause 为 errMid，root 和 c1 分支完全不受影响；对应 3 个 AfterFunc 各执行一次；
3. 再 GC，用 errRoot 取消 root；剩余 4 个节点退出且 cause 为 errRoot；已取消子树保持 errMid；
4. 预先 Stop 的回调执行次数必须为 0。

# 期望行为

1. 中间子树取消只产生 3 个 exit，cause 全部为 errMid；
2. root/c1/c1g0/c1g1 在中间取消后 `Err()==nil` 且 `Cause()==nil`，sibling 无 exit；
3. root 取消后产生 4 个 exit，cause 全部为 errRoot；
4. c0 子树 cause 不被后到的 root cause 覆盖；
5. 每个节点 AfterFunc 恰好执行 1 次，预先 Stop 的回调执行 0 次；
6. 取消前 GC、两次取消间 GC、最终 GC 后所有节点保留链完整；
7. 最后一行输出 `ALL PASS l04_context_cancel_cause_tree`。

# 失败判读

- cause 不符：WithCancelCause 首因/父因传播错误；
- 出现多余 exit 或 sibling 退出：取消范围泄漏到整棵树；
- AfterFunc 计数 >1 或 stopped 回调执行：注销/幂等取消失效；
- `chain corrupted`：阻塞于 Done 的 goroutine 栈指针未保留；
- 超时：子树未传播取消，或回调未执行。

# 参数

- `UAF_SEED`：各节点链值（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_context_cancel_cause_tree/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=8 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
