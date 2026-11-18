---
id: l09_cgo_errno
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo，重点库 go-ethereum/containerd/tailscale）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09 等维度）
tags: [L09, cgo, errno, syscall, ENOENT]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 go-ethereum/containerd 中“调用 C/系统库后检查 errno”的边界：在**同一次** C 调用内设置并读取 errno（errno 是线程局部的，跨 cgo 调用不能假定同一线程），以及真实 `fopen` 失败后立即取出 errno，再由 Go 侧用 `syscall.Errno` + `errors.Is(err, fs.ErrNotExist)` 判定。

# 期望行为

1. `c_errno_roundtrip` 对 EACCES/ENOENT/EINVAL/EPERM/0 返回相同值；
2. `fopen` 不存在的路径返回 -1，调用内与紧随其后读到的 errno 都是 ENOENT，且 Go 判定 `fs.ErrNotExist` 成立；
3. 清零 errno 后真实失败会重新置为 ENOENT；
4. 8 个 goroutine × 200 轮并发 roundtrip 结果稳定，GC 后仍成立；
5. 最后一行 `ALL PASS l09_cgo_errno`。

# 失败判读

- errno 不匹配：C 侧在设置后又被库函数覆盖，或 Go 侧读取出参的指针/类型错误；
- 跨 cgo 调用读 errno 不稳定：用例设计不允许依赖跨调用线程局部值（本用例只做调用内验证）；
- 崩溃：C 保留了 Go 栈指针（本用例的出参仅在调用期内使用）。

# 参数

需要 `CGO_ENABLED=1` 与 gcc/clang；`BUILD_MODE=module`。`CAPS=cgo` 可过滤本用例。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_errno
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
