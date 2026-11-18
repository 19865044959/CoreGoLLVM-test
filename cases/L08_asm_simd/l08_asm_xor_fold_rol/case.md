---
id: l08_asm_xor_fold_rol
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/badger/y/encrypt_test.go:27（XORBlock 原地加解密，异或是校验/混淆热路径）
  - research/repos/badger/y/encrypt_test.go:48（第二次异或同一流恢复原文）
tags: [L08, asm, amd64, xor, rotate, fold, checksum]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟协议/日志校验中的“异或折叠 + 循环左移”字节流摘要：`acc = ROL64(acc,1) ^ b` 逐字节累积，汇编用 `ROLQ $1` 与 `XORQ` 完成；与 Go 参考实现对照。异或折叠是 XOR 校验（同目录 `l08_asm_xor_bytes`）之外更强调状态联动与位序的变体。

# 期望行为

1. 种子 0/1/2/全 1/`1<<63`/黄金分割常数下，长度 0/1/2/3/7/8/15/16/17/31/32/33/63/64/65/255/256/1024/65537 的结果与参考实现一致；
2. 全 0x00 与全 0xff 的 4096 字节缓冲结果一致；
3. 空输入原样返回 seed（不访问内存）；
4. 子切片 `b[2:n-2]` 结果与参考实现一致；
5. 分块续算：按 1/2/7/16/33/128/1024/4096 字节切分，每段以上段结果作为 seed，最终值等于整段一次计算；
6. 每轮显式 GC + 垃圾分配后结果不变，源字节不被修改；
7. 最后一行 `ALL PASS l08_asm_xor_fold_rol`。

# 失败判读

- 结果等于按字节异或（无旋转）：`ROLQ` 缺失或写成了 `SHLQ`；
- 只有长输入错误：旋转作用于 32 位寄存器（`ROLL`），高 32 位被清零；
- 分块续算不一致：入口未使用传入 seed 作为 acc 初值；
- 空输入返回 0：缺少 `n<=0` 的入口检查或提前返回路径；
- 返回垃圾：未写回 ABI0 返回槽 `ret+24(FP)`；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_xor_fold_rol
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
