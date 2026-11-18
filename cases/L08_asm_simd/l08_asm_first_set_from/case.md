---
id: l08_asm_first_set_from
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/protobuf-go/internal/set/ints_test.go:24（位图 Set/Has/Clear，边界 0/63/64/1023）
  - research/repos/go-redis/bitmap_commands_test.go:80（BITCOUNT 的位/字节下标换算）
tags: [L08, asm, amd64, BSF, bitmap, first-set, range-scan]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟位图/空闲块分配器的“从指定位置起找首个置位”：起始位可能落在任意字内，首字用 `~0<<(start&63)` 掩掉起始位之前的位，`BSFQ` 定位最低置位；随后逐字扫描，全零或越界起点返回 -1。是 `l08_asm_first_set_bit`（从头扫描）的范围变体。

# 期望行为

1. nil、空、全零切片返回 -1；start 为负按 0 处理；start≥n*64 返回 -1；
2. 单个置位位于不同字/位（含 0/1/31/62/63/64/65/127/末位）时，starts 取 `-70..total+64` 的边界值，返回 `>=start` 的最小置位下标；
3. 1/2/3/5 字切片的字边界 63/64/65 与末尾位结果正确；
4. 交替位、稀疏位、末尾高位图的结果与 Go 逐位参考实现一致；
5. 50 组定种子伪随机位图在全部起点上一致；子切片外的字不被读取；
6. 每轮显式 GC + 垃圾分配后结果不变，源字不被修改；
7. 最后一行 `ALL PASS l08_asm_first_set_from`。

# 失败判读

- start 在字内时返回更早的置位：首字掩码未生成（变量移位用了错误的寄存器）或 `ANDQ` 方向写反；
- start 为 64 的倍数时结果少 64：字下标 `start>>6` 或位掩码 `start&63` 计算错误；
- 返回 -1 但存在置位：跳过首字后循环上界 `R9<n` 写错；
- 返回下标等于字下标而非位下标：漏乘 64 (`SHLQ $6`)；
- start 越界时越界读：入口未比较 `start` 与 `n*64`；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_first_set_from
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
