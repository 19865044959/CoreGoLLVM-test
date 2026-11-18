---
id: l05_closure_escape_alloc
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: pgconn/ctxwatch/context_watcher_test.go:26 的闭包回调捕获状态，取消/清理路径中存活
tags: [L05, closure, escape-analysis, captured-slice, pointer, gc]
requires: []
runtime: "<10s"
---

# 场景

`//go:noinline` 工厂函数 `makeCloser` 让闭包捕获堆上的 `*cnode`、计数器与可变长 `[]byte`，返回调用闭包与观察闭包；300 个闭包存入切片长期持有。每个闭包经过 4 轮、每轮不同增量的调用，期间穿插垃圾分配与 GC；最后校验捕获值、hist 长度与节点指针，并验证循环内闭包在循环结束后调用仍保留各自捕获值。

# 期望行为

1. 创建后每个被捕获节点字段正确；
2. 每轮调用返回值等于期望 `v + len(hist)`，4 轮后 `get()` 返回的 `v` 与 hist 长度符合累计期望；
3. 多轮 GC 后 `*cnode` 指针身份不变、`name/v` 未被改写；
4. 循环内创建的 16 个闭包在循环结束后调用，各自结果独立且正确；
5. 丢弃旧闭包后再 GC，新闭包状态从零开始，不复用旧捕获状态；
6. 最后一行输出 `ALL PASS l05_closure_escape_alloc`。

# 失败判读

- `captured node wrong` / `ret=... want=...`：闭包捕获变量或 hist 追加路径错误；
- `captured pointer corrupted after GC`：逃逸的捕获对象被提前回收；
- `late closure`：循环变量捕获语义错误（Go 1.22+ 每迭代变量）；
- 仅 stress 下失败：闭包环境对象的堆分配/扫描问题。

# 参数

- `UAF_SEED`：控制捕获初值与每轮增量（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 300 个闭包 × 4 轮调用。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_closure_escape_alloc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
