---
id: l09_cgo_strtok_r
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 字符串解析边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, strtok_r, tokenize, saveptr, in-place, CString, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库字符串分词 API 的绑定：Go 用 `C.CString` 分配 C 缓冲区，`strtok_r` 在调用期内原地把分隔符改写为 NUL 并返回指向缓冲区的 token 指针，Go 立即 `C.GoString` 复制。覆盖多分隔符、连续/首尾分隔符、空串、长 token、两个 `strtok_r` 流交错（独立 saveptr）以及 token 循环中的显式 GC。C 不保留任何 Go 指针。

# 期望行为

1. `"alpha,beta,gamma"` 按 `","` 切出 3 个 token，调用次数为 4（含最后返回 NULL 的一次）；
2. 缓冲区原位的两个分隔符字节被改写为 0，其余字节保持不变；
3. 多分隔符 `",; "` 跳过空 token，连续分隔符只把 token 后第一个分隔符写为 NUL，被跳过的分隔符保持原字节；
4. 无分隔符返回单个 token；全分隔符串与空串立即返回 NULL；
5. 200 字节长 token 与尾部 token 内容/长度正确；
6. 两个独立 saveptr 交错推进时 token 序列互不串扰；
7. token 循环中的显式 GC 不影响后续 token 指针与内容；
8. 最后一行 `ALL PASS l09_cgo_strtok_r`。

# 失败判读

- `basic tokens/call count`：C.CString 内容或分隔符指针错误，或 `strtok_r` 声明缺失；
- `delimiters rewritten` / `skipped delimiter`：对 strtok_r 原位改写与跳过规则的理解错误；
- `all delimiters/empty string`：未处理首字符即 NULL 的路径；
- `long token`：缓冲区截断或长度计算错误；
- `interleaved`：两个 saveptr 被复用/覆盖（非重入调用）；
- GC 后 token 错乱：C 保留 Go 指针，或在 free 之后继续读取（本用例 token 均在缓冲区释放前复制）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc（preamble 定义 `_GNU_SOURCE` 以启用 `strtok_r`）。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_strtok_r
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
