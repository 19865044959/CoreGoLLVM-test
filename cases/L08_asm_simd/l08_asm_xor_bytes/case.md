---
id: l08_asm_xor_bytes
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/badger/y/encrypt_test.go:27（XORBlock 原地加解密，要求对合且允许 dst 与 src 别名）
  - research/repos/badger/y/encrypt_test.go:48（第二次异或同一流恢复原文）
tags: [L08, asm, amd64, SSE2, PXOR, xor, in-place, sentinel]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟加密/校验场景中的双缓冲异或热路径（XOR 混淆、块密钥流）：16 字节 `MOVOU` 载入 + `PXOR` + `MOVOU` 写回，剩余不足 16 字节走标量尾部；允许 `dst` 与 `src` 指向同一缓冲区。用容量尾部哨兵检测越界写。

# 期望行为

1. 长度 0/1/2/7/8/15/16/17/31/32/33/63/64/65/255/256/1024 的两缓冲异或结果与 Go 逐字节参考实现一致；
2. `dst[0:n]` 之外的哨兵字节（0x5c）不被修改；
3. 源缓冲在调用前后不变；
4. `dst == src` 时结果全零，不崩溃；
5. 子切片偏移（dst 起点 +3、src 起点 +2、长度 n-5）结果正确；
6. 连续两次异或同一密钥流恢复原文（对合性）；
7. 每轮显式 GC + 垃圾分配后重新异或结果一致；
8. 最后一行 `ALL PASS l08_asm_xor_bytes`。

# 失败判读

- 尾部字节未异或：16 字节块循环后的标量尾未执行或条件写反；
- 哨兵被修改：尾部循环越界写，或块循环处理了超过 n 的整块；
- 源被修改：误把异或结果写回 `SI` 而非 `DI`；
- 别名场景结果非零：先写回 dst 再读取 src，同地址时第二次读已变值；应先载入两端再写回；
- 结果长度正确但内容随机：`PXOR` 操作数方向或 `MOVOU` 偏移错误；
- GC 后结果变化：Go 侧未保持 dst/src 存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_xor_bytes
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
