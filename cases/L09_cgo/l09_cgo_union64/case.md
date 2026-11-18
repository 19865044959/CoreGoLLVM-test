---
id: l09_cgo_union64
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 库联合体/紧凑布局）
tags: [L09, cgo, union, little-endian, punning]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库的 64 位联合体：`u64_t {u; double d; uint8_t b[8]; struct{lo,hi}}`。cgo 不能直接访问 union 字段，全部通过 C 辅助函数读写，验证四种视图的别名与小端位模式。

## 期望行为

1. sizeof=8、C alignof=8，Go `unsafe.Sizeof(C.u64_t{})` 为 8；
2. u=0x1122334455667788 时 b[0..7]=0x88..0x11，lo=0x55667788、hi=0x11223344；
3. 写 b[7]=0xAA 后 u=0xAA22334455667788；写 lo/hi 后对应视图更新；
4. 写 double 1.5 后 u 等于 math.Float64bits(1.5)；负零符号位为 0x8000000000000000；
5. GC 后负零与位模式不变；200 轮 u 读写 + 周期 GC 稳定；
6. 最后一行 `ALL PASS l09_cgo_union64`。

## 失败判读

- 字节序不符：目标不是小端或 C 编译器布局不同（用例限定 linux/amd64）；
- 负零错误：Go 常量 -0.0 实为正零，应使用 math.Copysign(0,-1)；
- cgo 直接访问 union 构建失败：应改用 C 辅助函数（本用例即此模式）；
- GC 后内容变化：C 栈/静态内存被误当作 Go 堆扫描（不应发生）。

## 参数

需要 `CGO_ENABLED=1` 与 gcc/clang；`BUILD_MODE=module`。断言以小端 + x86-64 布局为前提。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_union64
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
