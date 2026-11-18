---
id: l08_asm_overlap_copy
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:34（LZ 解码 length/offset 组合的 dst 边界与拷贝语义）
tags: [L08, asm, amd64, memmove, overlap, copy]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟解码器/缓冲管理中的“重叠区间拷贝”（memmove 语义）：手写 amd64 汇编检测 `src < dst < src+n`，命中时从尾部反向按 16 字节块拷贝，否则正向拷贝，尾部逐字节处理。与 Go 内建 `copy`（即 memmove）全量对照，覆盖原地、左重叠、右重叠、非重叠与 16/8/1 字节分段边界。

# 期望行为

1. 固定场景（原地全量/子区间、右重叠 1/7 字节、左重叠 1/7 字节、双向非重叠、零长度、覆盖到首尾）结果与 Go `copy` 完全一致；
2. 长度 0..70 的右重叠（src=10,dst=13）与左重叠（src=13,dst=10）结果均与 Go 一致；
3. 4096 字节大块右重叠与非重叠拷贝结果一致；
4. 每轮显式 GC + 垃圾分配后目标缓冲内容不变；
5. n=0 与 nil 指针不访问内存；最后一行 `ALL PASS l08_asm_overlap_copy`。

# 失败判读

- 右重叠（dst>src）内容呈重复噪声：未走反向拷贝，正向覆盖了尚未读取的源字节；
- 左重叠/非重叠错误：方向判断条件（`dst<=src`、`dst>=src+n`）写反；
- 尾部几个字节错误：16 字节块循环结束后单字节反向/正向步进写错；
- 越界写：区间外字节与 Go `copy` 结果不一致；
- GC 后内容变化：Go 侧未保持缓冲存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_overlap_copy
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
