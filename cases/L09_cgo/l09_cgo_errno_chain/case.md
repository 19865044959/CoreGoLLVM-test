---
id: l09_cgo_errno_chain
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 库 errno 检查）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，系统调用错误边界）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, errno, ENOENT, EISDIR, EBADF, syscall, errors.Is, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定系统调用的 errno 链：每个 C 辅助函数在**同一次调用内**设置 errno 并立即写入出参，Go 侧再映射为 `syscall.Errno` / `errors.Is`。覆盖 open 失败（空路径与不存在路径）→ ENOENT、读目录 fd → EISDIR、写只读 fd / 读已关闭 fd / 重复 close → EBADF、lseek(-1) → EBADF，以及 `strerror` 消息的重复调用稳定性、截断与零容量行为。C 不保留任何 Go 指针。

# 期望行为

1. C 暴露的 ENOENT/EISDIR/EBADF 数值与 Go `syscall` 常量一致；
2. 打开空路径与不存在路径失败，errno=ENOENT，且 `errors.Is(err, fs.ErrNotExist)` 成立；
3. 以只读方式打开 `"."` 成功；读取返回 EISDIR，写入返回 EBADF；
4. close 成功时 errno 清零；随后读同一 fd 与再次 close 都返回 EBADF；
5. `strerror` 对三种错误码返回非空且互不相同的消息，重复调用一致；容量不足返回 -1，容量 0 返回 -1；
6. 100 轮 open→close(-1)→lseek(-1) 错误链每轮都得到最新错误码，期间周期 GC 不影响结果；
7. GC 后同一组错误码与消息仍稳定；
8. 最后一行 `ALL PASS l09_cgo_errno_chain`。

# 失败判读

- `ENOENT/EISDIR/EBADF value`：Go 与 C 常量映射不一致（目标平台差异）；
- `open ...`：路径或 flags 错误，C 辅助函数未写 errno 出参；
- `read directory`：在非 Linux 上目录 read 语义不同（用例限定 Linux）；
- `write read-only/read closed/double close`：fd 生命周期或 Go 侧误用；
- `strerror ...`：缓冲区长度/截断处理错误，或错误消息依赖 locale 导致相同（消息文本不打印，仅比较）；
- GC 后错误：C 保留了 Go 指针（本用例 path/buffer 只在调用期内传递）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux（读目录返回 EISDIR）。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_errno_chain
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
