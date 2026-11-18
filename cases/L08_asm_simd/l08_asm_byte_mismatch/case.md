---
id: l08_asm_byte_mismatch
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:31（s2 汇编/纯 Go 双实现交叉验证，结果必须逐项一致）
tags: [L08, asm, amd64, SSE2, byte-compare, mismatch]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/索引库中的“字节比较”热路径：SSE2 每次装载 16 字节，`PCMPEQB` 得到相等掩码，取反后统计差异位数；尾部逐字节比较。与纯 Go 逐字节比较对照，覆盖块边界、差异位置、子切片起始偏移与 GC 后缓冲内容稳定。

# 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/63/64/65/255/1000 的两个相同缓冲返回 0；
2. 在首位/中位/末位翻转字节后，返回的差异数与 Go 参考实现一致；
3. 多字节翻转（含跨 16 字节块边界）结果一致；
4. 从 `&buf[1]` 开始、长度 `n-3` 的子切片比较结果与 Go 一致；
5. 每轮显式 GC + 垃圾分配后重复比较，结果不变且源缓冲不被修改；
6. nil + n=0 返回 0；最后一行 `ALL PASS l08_asm_byte_mismatch`。

# 失败判读

- 差异数偏小：`PMOVMSKB` 掩码取反遗漏（把相等位当成差异位），或 popcount 循环未累加；
- 16 字节边界附近错误：块循环剩余长度或尾部入口条件写错；
- 仅子切片错误：起始指针偏移计算错误；
- GC 后结果变化：Go 侧未保持缓冲存活；
- 崩溃：`MOVOU` 跨越 n 越界读取（本实现只整块读，不越过 n）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_byte_mismatch
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
