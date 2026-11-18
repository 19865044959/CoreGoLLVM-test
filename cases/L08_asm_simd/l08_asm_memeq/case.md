---
id: l08_asm_memeq
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:31（s2 汇编/纯 Go 双实现交叉验证）
tags: [L08, asm, amd64, SSE2, memequal, bytes-equal]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟哈希表/压缩库中“汇编 memequal”热路径：SSE2 每次装载 16 字节，`PCMPEQB` 后用 `PMOVMSKB` 判断是否全等，尾部按字节比较。与 `bytes.Equal` 对照。

# 期望行为

1. 长度 0/1/7/15/16/17/32/33/100 的相同缓冲区 asm 返回 true；
2. 在首位/中位/末位翻转一字节后 asm 返回 false（且与 `bytes.Equal` 一致）；
3. 不同内容缓冲区与 `bytes.Equal` 结果一致；
4. 每轮 GC 后重复比较结果不变，源缓冲区不被修改；
5. 最后一行 `ALL PASS l08_asm_memeq`。

# 失败判读

- 全等时返回 false：`CMPL DX,$0xffff` 掩码判断或广播错误；
- 差异未检出：`PCMPEQB` 操作数方向或块推进步长错误；
- 布尔返回为随机值：未显式写回 `ret+24(FP)`（ABI0 返回槽必须写）；
- 崩溃：读取超过 n 字节（本实现按 16 字节整块 + 标量尾部，不越界）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_memeq
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
