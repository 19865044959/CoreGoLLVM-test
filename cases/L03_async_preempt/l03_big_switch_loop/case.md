---
id: l03_big_switch_loop
dim: L03_async_preempt
priority: P0
sources:
  - caddy@v2.10.0: admin_test.go:51 /config/ JSON 树 POST/PUT/PATCH/DELETE，用 type switch 分派 map/数组/标量节点
  - sqlc@v1.28.0: internal/codegen/golang/result_test.go:10 命令类型表驱动 switch，判定各命令是否输出列
tags: [L03, async-preempt, big-switch, string-switch, tight-loop, pointer-hold, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

主 goroutine 在紧循环里对预生成的 32 个字符串键做 switch 分派，每个分支只计算一个节点偏移量，随后读取两个堆节点的字段；循环体没有函数调用。每 32768 轮显式 GC 并分配垃圾，GC pump 并发执行 8 轮 GC。

# 期望行为

1. 32 个 case 全部命中，default 分支计数为 0；
2. 循环至少执行 131072 轮；
3. 分支偏移校验和与参考值一致；
4. 主循环显式 GC 次数符合 `(done-1)/32768+1`；
5. GC pump 至少完成一轮；
6. 全部节点字段在 GC 后保持不变；
7. 退出码 0，最后一行 `ALL PASS l03_big_switch_loop`。

# 失败判读

- `FAIL: all switch cases hit labeled branches`：switch 分派表/字符串比较生成错误；
- `FAIL: big-switch pointer checksum`：分支内指针偏移或字段读取错误；
- `FAIL: switch-loop nodes intact`：GC 后节点被覆盖；
- `SIGSEGV` / `fatal error: found bad pointer`：分支汇合处局部指针未标记。

# 参数

- `UAF_SEED`：分别决定节点索引与分支索引的步长/偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_big_switch_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
