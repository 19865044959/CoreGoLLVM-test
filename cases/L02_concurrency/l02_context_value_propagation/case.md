---
id: l02_context_value_propagation
dim: L02_concurrency
priority: P0
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:126 取消与两次 select 竞争，状态与附属头部必须一致
  - etcd@v3.5.0: raft/node_test.go:80 select 监听 done 与 ctx.Done 解除阻塞，停止后保持幂等
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:123 Run 循环 select 收包/定时/取消，context 贯穿后台 goroutine
tags: [L02, context, WithValue, propagation, shadowing, sibling-isolation, cancel-survival, nil-key, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

验证 `context.WithValue` 的传播与隔离语义，覆盖真实服务里 metadata/请求值穿过 goroutine 与取消路径的行为：

- 构建 root(WithCancel) 下的两个兄弟分支 `sib1(keyA)`、`sib2(keyB)`，以及三层覆盖链 `br0(keyA=v10) → br1(keyB=v11) → br2(keyA=v12)`；
- 校验：root 看不到子值；兄弟互不可见；近端 keyA=v12 覆盖远端 v10、keyB 继承 v11；父节点看不到子节点附加的值；不同 key 类型的同名 name 不冲突；
- `WithValue` 不替换 `Done()` channel；在其上叠加已过期 `WithDeadline(time.Unix(0,0))`，立即得到 `DeadlineExceeded` 且值仍可读；
- nil key 必须 panic 且可 recover；nil value 合法，可按类型断言取回 typed nil；
- 从 `br2` 再挂 64 层深链，逐层校验值，并确认父链看不到最深值；
- 8 个 goroutine 各自在深链上附加 worker-local 值，等待 `cancelRoot()` 后统一放行；父 context 取消后所有值仍可读，子 context 的 `Err()` 为 `context.Canceled`，worker-local 值不泄漏回父链。

关键位置显式分配垃圾并 `runtime.GC()`，跨 GC 校验 `*blob` 指针。

# 期望行为

1. 值仅对自身及后代可见，兄弟分支和父链互相隔离；
2. 同名 key 的近端值覆盖远端值，未覆盖的键继续从祖先继承；
3. 不同 key 类型（struct 类型不同）不冲突；
4. `WithValue` 的 `Done()` 与父 context 是同一 channel；
5. 已过期 deadline context 立即 `DeadlineExceeded`，值照常可读；
6. nil key 触发 panic；nil value 可存储并按 typed nil 取回；
7. 64 层深链每层值正确，父链不可见深层值；
8. 父 context 取消后 8 个 goroutine 仍能读取值，worker-local 值不泄漏；payload 跨 GC 不变；
9. 最后一行输出 `ALL PASS l02_context_value_propagation`，退出码 0。

# 失败判读

- `root must not see child value` / `sibling ... must not see`：值链错误地共享或查找越界；
- `value mismatch for key ...`：覆盖顺序或祖先继承错误；
- `WithValue must not replace Done channel`：包装 context 未透传 Done；
- `missing value` 出现在取消后：取消路径错误地丢弃了值；
- `WithValue with nil key must panic`：nil key 校验缺失；
- `deep value N mismatch`：深链构造/查找错误；
- `payload corrupted after ... GC`：值指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定所有 `*blob` 的数值；
- 无 `TORTURE_*`；深链层数、worker 数与迭代均固定。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_context_value_propagation/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
