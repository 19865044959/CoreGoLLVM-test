---
id: l08_asm_hex_encode_upper
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/ent/schema/field/field_test.go:341（[]byte 字段以 hex.EncodeToString 落库）
  - research/repos/ent/schema/field/field_test.go:347（hex.DecodeString 读回，要求编码/解码往返一致）
tags: [L08, asm, amd64, hex, uppercase, nibble, encoding]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟二进制字段/摘要写入文本协议前的大写十六进制编码热路径：汇编逐字节把高、低半字节作为 16 项查表下标，映射到 `0-9A-F` 并写入目标缓冲；查表指针由 Go 侧传入。与 `strings.ToUpper(hex.EncodeToString(...))` 逐字节对照，并检查不写 `dst[2n]` 之后的哨兵。

# 期望行为

1. 长度 0/1/2/3/15/16/17/31/32/33/63/64/65/255/256/1024 的输入编码结果与参考实现完全一致；
2. 固定向量 `00 09 0a 0f 10 7f 80 ab cd ef f0 ff` 编码为 `00090A0F107F80ABCDEFF0FF`；
3. `dst[2n]` 与 `dst[2n+1]` 的哨兵字节不被修改；nil/空输入不写任何字节；
4. 大写十六进制能由 `hex.DecodeString` 解码回源数据（往返一致）；
5. 源数据编码前后不变，子切片 `src[2:n-1]` 编码正确；
6. 每轮显式 GC + 垃圾分配后重新编码结果不变，查表内容不被修改；
7. 最后一行 `ALL PASS l08_asm_hex_encode_upper`。

# 失败判读

- 输出的字母段为小写：查表内容或表指针传错（与小写版本混用）；
- 高低半字节颠倒：移位方向或 `(DI)`/`1(DI)` 写入偏移写反；
- 每字节只推进 1 个输出字节：目标指针步长错误，后一字符被覆盖；
- 哨兵被改写：循环多写一轮，或入口未对 n 做零值检查；
- 解码往返失败：某半字节映射到非十六进制字符，检查表长度与位掩码；
- GC 后结果变化：Go 侧未保持 src/dst/查表存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_hex_encode_upper
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
