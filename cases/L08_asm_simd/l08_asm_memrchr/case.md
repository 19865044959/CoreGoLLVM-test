---
id: l08_asm_memrchr
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/gitea/tests/integration/issue_test.go:43（LastIndexByte 取 URL 末段，定位最后一个分隔符）
  - research/repos/gitea/tests/integration/issue_test.go:181（从属性串尾部定位 '-'）
tags: [L08, asm, amd64, SSE2, memrchr, LastIndexByte, backward-scan]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟解析 URL/属性串时“从尾部找最后一个分隔符”的热路径：从缓冲区末尾起以 16 字节为块倒扫，`PCMPEQB` + `PMOVMSKB` 得到命中掩码后用 `BSRL` 取块内最高位（最靠右的命中）；剩余不足 16 字节时逐字节倒扫。语义与 `bytes.LastIndexByte` 一致。

# 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/64/65/257 且目标字节多次出现时，返回最后一次出现的下标；
2. 目标字节位于首位、中位、末位、只出现一次、完全不出现时结果与 `bytes.LastIndexByte` 一致；
3. 全 0xff、全 0x00、含 0x00 的缓冲区处理正确；
4. 子切片 `b[2:n-1]` 上的结果一致；
5. 每轮显式 GC + 垃圾分配后结果不变，源字节不被修改；
6. nil + n=0 返回 -1 且不访问内存；
7. 最后一行 `ALL PASS l08_asm_memrchr`。

# 失败判读

- 多次出现时返回较早位置：`BSRL` 误用 `BSFL`，取到了块内最左命中；
- 返回下标偏 16 的倍数：块尾指针 `SI` 与原始基址 `R9` 的减法错误；
- 首块即越界崩溃：块循环前未把 `SI` 置为 `p+n`，或 `n<16` 时误入块路径；
- 尾块漏扫：剩余长度 `CX` 在块循环后未正确递减；
- 下标为垃圾值：命中分支未写回 ABI0 返回槽 `ret+24(FP)`；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。目标字节仅在 0..255 范围内使用。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_memrchr
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
