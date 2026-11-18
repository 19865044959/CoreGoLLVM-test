---
id: l08_asm_bytes_index
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/fasthttp/header_test.go:325（原始 header 块逐字节扫描字段分隔符）
  - docs/research/fasthttp.md:39（raw header 与规范化视图共享缓冲，需按字节定位）
tags: [L08, asm, amd64, SSE2, bytes.Index, substring-search, PCMPEQB]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 HTTP header/协议解析中的子串定位热路径：先用 SSE2 `PCMPEQB` + `PMOVMSKB` 在 16 字节块内筛出“首字节等于 needle[0]”的候选位置，再用 `BSFL` 逐个取出候选并逐字节校验整段 needle；不足 16 个候选起点时走标量尾部。语义与 `bytes.Index` 一致：空 needle 返回 0，未命中返回 -1。

# 期望行为

1. nil/空 hay 与空 needle 的组合返回 0；空 hay 配非空 needle 返回 -1；
2. 长度 1..257、needle 长度 1..17 的所有组合中，首位/中位/末位命中及需检查超过 16 字节 hay 的场景，返回值与 `bytes.Index` 一致；
3. needle 首字节命中但后续字节不匹配（假候选）时返回 -1，不产生越界读；
4. 大量同首字节（全 'A'）的 hay 在 15/16/17/31/32/33/63/64/65 长度下，命中与未命中结果均正确；
5. 子切片 `hay[1:n-2]` 与含 0x00 的 needle 结果一致；
6. 每轮显式 GC + 垃圾分配后重复调用结果不变，hay/needle 内容不被修改；
7. 最后一行 `ALL PASS l08_asm_bytes_index`。

# 失败判读

- 块边界附近漏命中：候选起点计数 `n-m+1` 计算错误，或 `BTRL` 清位后未继续检查剩余候选；
- 返回位置偏 16 的倍数：`R8` 已扫描字节数与块内偏移 `R9` 相加错误；
- 假候选被误报命中：整段校验的 needle 指针或长度错误；
- 长度小于 needle 时崩溃/返回非 -1：入口未比较 `n` 与 `m`；
- 尾部返回垃圾下标：尾部循环未在每次迭代前检查剩余候选数 `CX`，越界读取相邻内存；
- GC 后结果变化：Go 侧未保持 hay/needle 存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_bytes_index
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
