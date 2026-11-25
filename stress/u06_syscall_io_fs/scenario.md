---
id: u06_syscall_io_fs
priority: P1
covers: [L19, L05]
requires: [unix]
default_seconds: 60
max_workers: 16
---

# 场景

syscall / 文件 I/O 长跑（Unix 优先）：四类 worker（`kind = id % 4`）并发推进，所有 open/close/pipe 创建与 fd 复用断言共用一把 `fdGate` 串行化（I/O 本身保持并发），排除并发 open 对 fd 编号的干扰；独立 gcChurn 持续 GC。

每 worker 每轮：

1. **pipe**：`os.Pipe` 建管道，writer goroutine 分块写入确定性 pat 序列（阶段 1：32×4KB；阶段 2：48×8KB），reader 主 goroutine 用 `io.ReadFull` 逐块读回并校验 FNV checksum / 首尾字节；writer 因管道容量阻塞与 reader 消费真实并发。
2. **file**：临时目录中创建文件，大 payload（阶段 1：32×4KB；阶段 2：24×8KB）分块写入 → `Seek(0)` → **显式 GC（fd 打开期间）** → 同一 fd 分块读回逐块校验 → 关闭并删除。
3. **syscall**：`syscall.Syscall(SYS_WRITE/SYS_READ)` 直接读写管道（fd 来自 `os.File.Fd()`，缓冲指针经 `unsafe.Pointer` 转 `uintptr` 传入，调用后 `runtime.KeepAlive`）；每 4 块显式 GC 一次，验证指针参数/缓冲跨 GC 后仍能正确读回。系统调用号按 GOOS/GOARCH 表选取；Windows/未知平台跳过该子模式（记录 `syscall_skipped`）。
4. **fd**：在 `fdGate` 内 open → 记录 fd → close → **显式 GC** → 再次 open，断言拿回**同一 fd**（Unix；Windows/Plan9 等跳过，记录 `fd_skipped`）；随后在新 fd 上写入、再 GC 一次、Seek 回读校验，覆盖 fd 生命周期与 GC 交互。
5. 阶段边界检查临时目录为空（无 fd/文件泄漏）；收尾 `RemoveAll` 后校验目录确实不存在。

实现备注：`syscall.Syscall` 的平台签名不同（unix 4 参数 / Windows 6 参数），单文件直接调用无法跨平台编译；本场景用 `reflect.ValueOf(syscall.Syscall)` 在运行时按平台调用，unix 上仍是原始 `syscall.Syscall` 内核路径。两个阶段按固定轮数或运行时长均分，阶段 2 放大块与 payload。

## 不变量

1. pipe 每个数据块 FNV checksum 与确定性 pat 序列一致（`pipe-chunk`），写入/读取字节数完整，无短写/短读；
2. 文件 roundtrip 每块内容与 pat 序列一致（`file-chunk`），fd 在写入与读取之间的显式 GC 后仍有效；
3. `syscall.Syscall` 原始读写返回完整字节数且 errno==0（EINTR 重试），缓冲指针参数跨 GC 后读回内容正确（`syscall-chunk`）；
4. Unix 上 close→GC→open 必须返回同一 fd（`fd-reuse`）；复用后的 fd 在再次 GC 后仍可读写；
5. 阶段边界临时目录为空；收尾目录已删除（`os.Stat` 返回 IsNotExist）；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started == finished == workers × phases`；
7. 出现对应 kind 时其子模式必须至少执行一次（或在不支持的平台记录 skip），`chunk_checks` 非零；
8. 收尾 quiesce 后 goroutine 回基线（+8 容差）；
9. 最后一行 `STRESS PASS u06_syscall_io_fs`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 8 | worker 数（≤16，按 `id%4` 分配四类负载） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL + 全栈 |
| `UAF_SEED` | 1 | pat/checksum 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `pipe-chunk: checksum=...` / `pipe-read` / `pipe-write`：管道读写并发、fd 生命周期或缓冲内容损坏；
- `file-chunk` / `file-read`：文件 roundtrip 内容错、fd 在 GC 后失效或分块边界错误；
- `syscall-write` / `syscall-read`：原始系统调用失败（trap 号或 fd 错误）或 EAGAIN/EINTR 处理不当；
- `fd-reuse: fd not reused across GC`：close/GC/open 后未拿回同一 fd（Unix）；检查是否有未经 `fdGate` 的 open/close；
- `temp dir ... not empty` / `still exists after cleanup`：文件泄漏或清理失败；
- `no worker progress`：watchdog 超时（I/O 或 GC 卡死），附全栈；
- `goroutines baseline ... leak`：writer/reader goroutine 泄漏；
- 仅在 soak/soak-single 下失败：优先怀疑 GC 与 fd/缓冲生命周期的交互（clobberfree 更敏感）。

## 运行

```bash
SUITE=stress FILTER=u06 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
