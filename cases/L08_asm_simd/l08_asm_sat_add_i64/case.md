---
id: l08_asm_sat_add_i64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/protobuf-go/encoding/protowire/wire_test.go:652（ZigZag 编码覆盖 MinInt64/MaxInt64 边界）
  - research/repos/ent/entc/integration/type_test.go:65（可选 int64 字段写入 MinInt64 极值）
tags: [L08, asm, amd64, saturating-add, overflow, int64, maxmin]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟计数器/资源计量中的“有符号饱和加法”：先做普通 `ADDQ` 得到可能回绕的和，再用 `(a^sum)&(b^sum)` 的符号位判断是否溢出；溢出时按 `a` 的符号选择 `MaxInt64` 或 `MinInt64`。与 Go 参考实现对照，重点覆盖紧贴 `math.MinInt64`/`math.MaxInt64` 的输入对。

# 期望行为

1. 13 个边界值（MinInt64、MinInt64+1、MinInt64+2、-2^40、-2、-1、0、1、2、2^40、MaxInt64-2、MaxInt64-1、MaxInt64）两两组合的结果与参考实现一致；
2. 2000 组定种子伪随机 int64 对及其构造的近边界对均一致；
3. 交换律成立：`satAdd(a,b) == satAdd(b,a)`；
4. 不溢出区间（-100..100 两两组合）结果等于普通加法；
5. 溢出时结果被钳制在极值，绝不回绕；
6. 每轮显式 GC + 垃圾分配后结果不变；
7. 最后一行 `ALL PASS l08_asm_sat_add_i64`。

# 失败判读

- 正溢出返回 MinInt64：溢出后按符号选择的分支条件写反；
- 负溢出返回 MaxInt64：同上，或 `TESTQ` 后用了错误的跳转条件；
- 溢出判定漏报：`(a^sum)&(b^sum)` 少了一个异或项，或 `ANDQ` 操作数顺序导致符号位丢失；
- 所有结果都等于普通加法：溢出检测后的 `JNS` 永不/始终跳转；
- 边界对结果正确但随机对错误：极值立即数加载错误（`0x8000000000000000` 需按 64 位加载，不能只用 32 位）；
- GC 后结果变化：参数按值传递，检查测试侧是否复用了被覆盖的变量。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_sat_add_i64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
