---
id: l08_asm_index_any2
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/fasthttp.md:39（原始 header 块逐字节扫描，同时识别 CR/LF 等分隔字节）
  - research/repos/gitea/modules/highlight/highlight_test.go:19（按 '\n' 切分行，单字节查找是切分热路径）
tags: [L08, asm, amd64, SSE2, PCMPEQB, two-delimiters, byte-index]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 header/JSON 扫描中“查找首个属于两个分隔字节之一”的热路径：每 16 字节装载一次，对 b0、b1 各做一次 `PCMPEQB` 后 `POR` 合并掩码，`PMOVMSKB` + `BSFL` 取最靠前命中；不足 16 字节逐字节扫描。语义等价于 `min(bytes.IndexByte(b,b0), bytes.IndexByte(b,b1))`（忽略负值）。

# 期望行为

1. nil/空缓冲返回 -1，不访问内存；单字节缓冲命中/缺失正确；
2. b0==b1 时退化为单字节查找，命中与缺失均正确；
3. 长度 1..255（含 15/16/17/31/32/33/63/64/65 块边界）下，分隔字节位于首位/中位/末位时返回值与参考实现一致；
4. 两个分隔字节同时出现时返回较小下标，交换 b0/b1 传参顺序结果不变；
5. 全为分隔字节时返回 0；含 0x00 的分隔字节处理正确；
6. 每轮显式 GC + 垃圾分配后结果不变，源缓冲不被修改；子切片结果一致；
7. 最后一行 `ALL PASS l08_asm_index_any2`。

# 失败判读

- 只识别 b0（或 b1）：少做了一次 `PCMPEQB`，或 `POR` 的操作数方向错误；
- 返回下标偏 16 的倍数：块内位下标未加已扫描字节数 `R8`；
- 末位命中漏报：尾部标量循环的步进/计数错误；
- b0==b1 时结果异常：广播向量生成路径只覆盖了一个参数；
- GC 后结果变化：Go 侧未保持切片存活，或缓冲区被回收复用。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_index_any2
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
