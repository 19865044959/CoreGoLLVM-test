---
id: l08_asm_minmax_u8
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/sonic.md:165（手写 SSE/AVX 汇编函数需独立验证指令级语义与 ABI）
tags: [L08, asm, amd64, SSE2, PMINUB, PMAXUB, min-max]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟编解码器/量化路径中的“汇编求字节极值”：SSE2 用 `PMINUB`/`PMAXUB` 逐 16 字节维护最小/最大向量，循环结束后用 `PSRLDQ` 水平归约，尾部逐字节比较。与纯 Go 无符号字节遍历对照，覆盖递增、常量、交替、稀疏模式与 16 字节块边界。

# 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/64/255/1000 的 seq/zero/ff/alt/sparse 五类模式，asm 的 (min,max) 与 Go 一致；
2. 空输入返回 (0,0)，不访问内存；
3. 从 `&b[1]` 开始、长度 `n-2` 的子切片极值与 Go 一致；
4. 每轮显式 GC + 垃圾分配后源字节不变且极值结果不变；
5. nil + n=0 返回 (0,0)；最后一行 `ALL PASS l08_asm_minmax_u8`。

# 失败判读

- 最小值偏大/最大值偏小：`PMINUB`/`PMAXUB` 操作数方向写反，或向量初值未全 0xff/全 0；
- 水平归约错误：`PSRLDQ` 步长或最终 `ANDQ $0xff` 缺失；
- 尾部分支错误：逐字节比较条件或步进写错；
- 块边界错误：剩余长度 `<16` 的判断写错；
- GC 后错误：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_minmax_u8
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
