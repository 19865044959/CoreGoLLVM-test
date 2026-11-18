---
id: l08_asm_struct_array_sum
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/gorm.md:40（Dest 为 slice/array/struct 时的元素遍历与字段取值）
tags: [L08, asm, amd64, struct-array, field-offset, GC]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 ORM/序列化路径中的“结构体数组求和”：`item{score int64; name string; weight int64}` 按 32 字节 stride 排列，汇编按偏移 0 与 24 累加两个数值字段；数组中每个元素都含 string 指针，GC 需要扫描数组。用于验证 ABI0 参数指针映射、Go 结构体布局与汇编偏移一致，以及 GC 后元素内容完整。

# 期望行为

1. Go 布局断言：score/name/weight 偏移为 0/8/24，sizeof=32；
2. 长度 0/1/2/7/64/257 的数组，asm 返回的 (score 和, weight 和) 与 Go 遍历一致（含负值）；
3. 从 `&items[1]` 开始、长度 `n-2` 的子数组求和与 Go 一致；
4. 每轮显式 GC + 垃圾分配后，asm 求和不变，所有元素的 score/name/weight 完好；
5. nil + n=0 返回 (0,0)；最后一行 `ALL PASS l08_asm_struct_array_sum`。

# 失败判读

- 求和偏差：stride（32）或字段偏移（0/24）与 Go 布局不一致；
- 第二字段结果错乱：误读 name 指针位置或 `MOVQ` 偏移写错；
- GC 后名字乱码/崩溃：数组未被保持存活，或汇编参数指针映射缺失；
- 仅子数组错误：起始指针偏移计算错误；
- 构建失败：非 amd64（`CAPS=asm,amd64` 过滤）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_struct_array_sum
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
