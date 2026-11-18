---
id: l08_asm_first_set_bit
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/protobuf-go/internal/set/ints_test.go:24（位图按 64 位字哈希/置位，需正确换算字与位下标）
  - research/repos/go-redis/bitmap_commands_test.go:80（BITCOUNT 按字节/位范围统计，位下标语义敏感）
tags: [L08, asm, amd64, BSF, bitmap, first-set-bit, word-scan]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟位图/空闲块分配器的“找第一个置位”热路径：逐 64 位字扫描，前导全零字直接跳过，首个非零字用 `BSFQ` 定位最低置位，返回全局位下标 `wordIndex*64 + bit`；全零或空输入返回 -1。

# 期望行为

1. nil、空切片、全零切片返回 -1；
2. 首个置位位于第 w 个字、位 0/1/31/32/62/63 时，返回 `w*64+bit`（w=0..7）；
3. 首字含任意单个置位（位 0..63）时不被后续全 1 字干扰；
4. 尾部第 1..17 个字才出现最高位置位时返回正确下标，验证多字扫描与偏移；
5. 40 组定种子伪随机字（部分字清零）的结果与 Go 参考实现一致；
6. 子切片 `s[1:n-1]` 的结果与参考实现一致；
7. 每轮显式 GC + 垃圾分配后结果不变，源字不被修改；
8. 最后一行 `ALL PASS l08_asm_first_set_bit`。

# 失败判读

- 返回 -1 但存在置位：字扫描循环提前退出，或 `TESTQ` 后跳转条件写反；
- 返回下标等于字下标而非位下标：漏乘 64（`SHLQ $6`）；
- 返回下标多 1：`BSFQ` 后额外加 1，或字下标从 1 开始；
- 只检查首字：循环计数 `CX` 递减错误，或 `SI` 步长不是 8；
- 高位置位时结果错误：误用 `BSRQ` 取最高位而不是 `BSFQ` 取最低位；
- 返回垃圾：未写回 ABI0 返回槽 `ret+16(FP)`；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_first_set_bit
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
