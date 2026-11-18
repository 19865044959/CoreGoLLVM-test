---
id: l04_goroutine_local_captures
dim: L04_scheduler_stack
priority: P0
sources:
  - pion/webrtc@v4.1.2: peerconnection_renegotiation_test.go:970 500 条 track 逐个绑定独立对象并跨事件循环推进
  - caddy@v2.10.0: modules/caddyhttp/templates/tplcontext_test.go:190 嵌套 include 与对象池 buffer 的独立上下文
tags: [L04, closure, capture, goroutine-local, gosched, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 WebRTC 500 track 与 Caddy 多模板 include 的“每个 goroutine 独立闭包上下文”：24 个 worker 各自构造 3 节点指针链和 4 槽堆切片，内部闭包 `inner` 捕获这两个对象；循环 48 步，每步写入本 worker 独有的值并累加，每 8 步 `Gosched` 一次。走到一半时全部阻塞，主协程 GC 两次后放行。闭包捕获的指针若被错误共享/覆盖，校验和会串号。

# 期望行为

1. 每个 worker 的 `verify` 在中点与 GC 唤醒后都看到自己的 3 节点链（校验和 `3*base+3`）；
2. 闭包 `inner` 每步只读写本 goroutine 的 `vals` 槽，无跨 worker 串号；
3. 最终累加器等于独立公式 `Σ(chainSum + base + step)`；
4. 24 个 worker 结果齐全，无丢失/重复；
5. 最后一行输出 `ALL PASS l04_goroutine_local_captures`。

# 失败判读

- `captured pointers mixed up`：闭包上下文被错误共享，或捕获指针在 GC 后失效；
- `acc=... want=...`：某步读到其他 goroutine 的数据或丢失更新；
- `missing result`：worker 未汇合；
- 仅 stress/k8s 下失败：GC 扫描闭包上下文（heap 对象）时的写屏障/指针位图问题。

# 参数

- `UAF_SEED`：每个 worker 的 base 与节点初值（默认 1）；固定 24×48 次迭代。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_goroutine_local_captures/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
