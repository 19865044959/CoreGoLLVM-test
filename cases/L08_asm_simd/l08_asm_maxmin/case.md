---
id: l08_asm_maxmin
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:60（BMI2 汇编路径与通用路径需要结果一致）
tags: [L08, asm, amd64, max-min, branch]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟编解码器/量化路径中“汇编求极值”循环：`CMPQ` 后条件更新累加器，覆盖有序、逆序、全相等、`math.MinInt64`/`math.MaxInt64` 与定种子伪随机序列；同时验证子切片偏移。

# 期望行为

1. 字符串中列出的固定序列及 8 组定种子伪随机序列，asm 最大值/最小值与 Go 实现一致；
2. 空切片返回 0（max 与 min 均为 0）；
3. 从 `&s[1]` 开始、长度 `n-2` 的子切片结果与 Go 一致；
4. 每轮 GC 后重复计算一致；
5. 最后一行 `ALL PASS l08_asm_maxmin`。

# 失败判读

- 极值错误：`CMPQ` 操作数方向或 `JLE`/`JGE` 分支条件写反；
- 空切片崩溃：入口未检查 n；
- 子切片错误：起始指针偏移；
- GC 后错误：Go 侧切片未保持存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_maxmin
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
