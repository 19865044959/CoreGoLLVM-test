---
id: l08_asm_abs_diff_u8
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/sonic.md:165（手写 SSE 汇编函数需独立验证指令级语义与 ABI）
  - docs/research/klauspost-compress.md:31（汇编实现与纯 Go 实现交叉验证，结果必须逐项一致）
tags: [L08, asm, amd64, SSE2, PMAXUB, PMINUB, PSUBUSB, abs-diff]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟图像差分/量化误差计算中的逐元素绝对差 `|a-b|`：16 字节块内 `PMAXUB`/`PMINUB` 取两端较大/较小值，再 `PSUBUSB` 得差（范围 0..255，不会下溢）；尾部逐字节比较相减。与 Go 参考实现对照，并穷举全部 256x256 输入对。

# 期望行为

1. 65536 组输入对（a 为高字节、b 为低字节）的绝对差逐字节与参考实现一致，交换操作数再验一遍；
2. 长度 0/1/2/3/7/8/15/16/17/31/32/33/63/64/65/255/1024 的边界测试中，`dst[0]`/`dst[n+1]` 哨兵不被改写；
3. 全 0 与全 255 缓冲的绝对差全为 255；相同缓冲全为 0；
4. `dst==a`、`dst==b` 的原地调用结果正确（先装载后写入）；
5. 子切片 `a[2:n-1]`/`b[2:n-1]` 计算正确；每轮显式 GC + 垃圾分配后结果不变，源数据不被修改；
6. nil + n=0 不访问内存且不写哨兵；
7. 最后一行 `ALL PASS l08_asm_abs_diff_u8`。

# 失败判读

- 差值方向错误（b-a 而非 |a-b|）：`PMAXUB`/`PMINUB` 的操作数顺序写反，或标量尾交换分支条件错误；
- 差值为 0 或 255 固定值：`PSUBUSB` 的源/目标方向写反（结果被钳到边界）；
- 只有尾部字节错误：标量尾的 `CMPQ/JAE` 条件或交换逻辑错误；
- `dst==a` 结果错误：先写回 dst 再读取 b，同地址第二次读已变值；应先装载两个向量；
- 哨兵被改写：尾部循环多写一轮，或块循环处理了超过 n 的整块；
- GC 后结果变化：Go 侧未保持三个切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_abs_diff_u8
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
