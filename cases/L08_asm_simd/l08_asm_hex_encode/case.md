---
id: l08_asm_hex_encode
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/ent/schema/field/field_test.go:341（[]byte 字段以 hex.EncodeToString 落库）
  - research/repos/ent/schema/field/field_test.go:347（hex.DecodeString 读回，需编码/解码往返一致）
tags: [L08, asm, amd64, hex, nibble, encoding]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟二进制字段/摘要写入文本协议前的十六进制编码热路径：汇编逐字节把高、低半字节映射为 `0-9a-f`（`ADDQ $48` 后若大于 `'9'` 再加 39 跳到 `a-f`），写入目标缓冲。与 `encoding/hex.EncodeToString` 逐字节对照，并检查编码器不写过 `2n` 边界。

# 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/255/256/1024 的输入编码结果与 `hex.EncodeToString` 完全一致；
2. 固定向量 `00 09 0a 0f 10 7f 80 ab cd ef f0 ff` 编码为 `00090a0f107f80abcdeff0ff`；
3. 目标缓冲在 `dst[2n:]` 的哨兵字节不被修改（检测越界写）；
4. 源数据编码前后不变，子切片 `src[2:n-1]` 编码正确；
5. 每轮显式 GC + 垃圾分配后重新编码结果不变；
6. nil/空输入不访问内存，不写哨兵；
7. 最后一行 `ALL PASS l08_asm_hex_encode`。

# 失败判读

- 字母段输出错误：半字节大于 9 时未加 `'a'-'0'-10`，或分支条件把 9 判成字母；
- 高低半字节颠倒：移位方向或写入偏移 `(DI)` / `1(DI)` 写反；
- 偶数位被覆盖：每字节目标指针只前进 1；
- 哨兵被改写：循环多写一轮，或入口未对 n 做零值检查；
- GC 后结果变化：Go 侧未保持 src/dst 存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_hex_encode
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
