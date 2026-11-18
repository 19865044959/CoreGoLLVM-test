---
id: l08_asm_sat_add_u8
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/sonic.md:165（手写 SSE 汇编函数需独立验证指令级语义与 ABI）
  - docs/research/klauspost-compress.md:60（BMI2/汇编路径与通用路径结果必须一致）
tags: [L08, asm, amd64, SSE2, PADDUSB, PSUBUSB, saturating, uint8]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟图像/音频量化中的无符号字节饱和加与饱和减：16 字节块分别使用 `PADDUSB`（加和钳到 255）与 `PSUBUSB`（差钳到 0），尾部逐字节比较钳制；允许 `dst` 与 `a` 或 `b` 指向同一缓冲。与 Go 参考实现对照，并穷举全部 256x256 输入对。

# 期望行为

1. 65536 组输入对（a 为高字节、b 为低字节）的饱和加与饱和减结果逐字节与参考实现一致，并交换操作数再验一遍；
2. 长度 0/1/2/3/7/8/15/16/17/31/32/33/63/64/65/255 的边界测试中，`dst[0]`/`dst[n+1]` 哨兵不被改写；
3. 饱和加可交换；饱和减在 `x<y` 时返回 0、否则返回 `x-y`；
4. `dst==a`、`dst==b` 的原地调用结果正确（先装载后写入）；
5. 子切片 `a[2:n-1]`/`b[2:n-1]` 原地计算正确；每轮显式 GC + 垃圾分配后结果不变，源数据不被修改；
6. nil + n=0 不访问内存且不写哨兵；
7. 最后一行 `ALL PASS l08_asm_sat_add_u8`。

# 失败判读

- 加法回绕到小值：误用了 `PADDB`，未使用饱和指令 `PADDUSB`；
- 减法下溢为大值：误用了 `PSUBB`，未使用 `PSUBUSB`；
- 只有尾部字节错误：标量尾的 255/0 钳制条件写反（`JBE`/`JNS`）；
- `dst==a` 结果错误：先写回 dst 再读取 b，同地址第二次读已变值；应先装载两个向量；
- 哨兵被改写：尾部循环多写一轮，或块循环处理了超过 n 的整块；
- GC 后结果变化：Go 侧未保持三个切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_sat_add_u8
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
