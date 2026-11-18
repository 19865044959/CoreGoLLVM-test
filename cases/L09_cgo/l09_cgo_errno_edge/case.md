---
id: l09_cgo_errno_edge
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 库调用后检查 errno 是常见边界）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, errno, ERANGE, pthread, thread-local, syscall]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定中“调用 C 库后检查 errno”的边界，但聚焦错误码的数值语义与线程局部性：`strtol` 上溢/下溢必须返回 `LONG_MAX/LONG_MIN` 并置 `errno=ERANGE`，成功路径不残留错误；`strtod` 上溢返回 `HUGE_VAL` 且 `errno=ERANGE`；另外在 C 侧创建两个 pthread，各自写入不同的 `errno`，用 barrier 保证写入交错后各自读回自己的值，验证 `errno` 的线程局部隔离。C 只同步写 Go 栈上出参，不保留 Go 指针；pthread 只使用 C 栈上的参数结构。

# 期望行为

1. C `long` 在当前目标为 64 位；
2. `strtol` 上溢返回 `LONG_MAX`、errno 为 `ERANGE`、消费全部输入；
3. `strtol` 下溢返回 `LONG_MIN`、errno 为 `ERANGE`；
4. `strtol("42xyz")` 返回 42、errno 清零、end 偏移为 2；
5. `strtod("1e99999")` 返回 `+Inf`、errno 为 `ERANGE`；
6. 两个 C pthread 交错写入/读取 errno，各自读回自己的错误码；
7. errno 可映射回 Go `syscall.Errno` 且错误消息非空；
8. GC 后上述行为不变；
9. 最后一行 `ALL PASS l09_cgo_errno_edge`。

# 失败判读

- `returns LONG_MAX/LONG_MIN`：C 库 strtol 范围语义差异，或返回值被截断为 32 位；
- `errno ERANGE`：C 调用后未在**同一次调用内**读取 errno，或目标平台不设置 ERANGE；
- `success leaves errno clear`：成功路径残留了上次错误；
- `pthread ... sees ...`：errno 不是线程局部量（或 barrier 同步失败导致读到对方值）；
- `maps to syscall.Errno`：Go 侧 errno 数值与 Linux 常量不一致；
- 链接失败：缺少 `-pthread`（本用例已加 `#cgo LDFLAGS`）；
- 挂起：C pthread 创建/barrier 失败导致测试阻塞（检查环境）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc（`pthread_barrier_t` 由 `_GNU_SOURCE` 启用）。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_errno_edge
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
