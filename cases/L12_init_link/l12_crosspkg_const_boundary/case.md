---
id: l12_crosspkg_const_boundary
dim: L12_init_link
priority: P1
sources:
  - grpc-go@v1.71.1: codes/codes_test.go:51 want := []Code{OK, NotFound, Internal, Canceled} 使用跨包 typed 常量
  - caddy@v2.10.0: admin_test.go:181 const key = "/" + rawConfigKey + "/apps/foo" 常量字符串拼接
  - gitea@v1.23.5: modules/templates/helper_test.go:81 静态表中出现 [0]int{} 零长度数组边界
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:43 `(1 + 2.0) / 3` 常量表达式类型提升
tags: [L12, module, constants, const-chain, cross-package, typed-constants, zero-length-array, int64-boundary, complex, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

`limits` 子包导出 uint8/int8/uint16 类型化常量的极值、int64/uint64 边界常量、字符串/复数常量表达式；`derive` 子包把这些常量继续用于 const 表达式与数组长度，包括 `[limits.ZeroLen]int` 零长度数组；main 再用两包常量之和定义 1122 项静态表并在 init 中填表。验证跨包常量传播、typed 常量类型保持、常量溢出边界、零长度数组与运行期截断。

## 期望行为

1. `limits`：ZeroLen/Slots/Blocks/Total 为 0/17/64/1088；MaxWidth/MinLevel 为 255/-128；FlagsAll 为 33023；Word 为 `go-l12`、WordLen 6；Name 为 `go-l12/derived`、NameLen 14；Unicode 字节长 6；CAbs2 25；
2. `Shifted` 为 uint64 `1<<63`，MinI64/MaxI64 为 int64 边界，Huge 为 `1<<40`，ExactFloat 为 0.375；
3. `%T`：MaxWidth 为 `limits.Width`、MinLevel 为 `limits.Level`、FlagsAll 为 `limits.Flags`、Shifted 为 uint64、CVal 为 complex128、HighBit/ExactFloat 为 int/float64；
4. `derive`：Slots 34、Span 1071、DerivedName 为 `go-l12/derived/d` 且长 16、LowMask 255 且类型仍为 `limits.Flags`、LevelSpan 383、ShiftCheck 128；
5. `TableLen==1122`；`len(derive.Zero)==0`、`len(derive.Grid)==64`、`len(derive.Rows)==len(derive.Labels)==34`、`len(table)==1122`；
6. `derive.InitOK` 为真；Grid[i]==i*3+32、Rows[i][1]==uint8(i*3+255)、Labels[i]=="r%02d"、table[i]==i%17+32；
7. typed 常量作 map key 与 switch 分支正确；运行期转换 `Width(300)==44`、`Level(-300)==-44`、`Flags(0x1ffff)==65535`；
8. 8 goroutine 并发读常量派生表 + 显式 GC 后内容不变；
9. 最后一行 `ALL PASS l12_crosspkg_const_boundary`。

## 失败判读

- 常量值/类型错误：跨 package 常量传播、常量折叠或 typed 常量类型保持错误；
- 零长度或数组长度编译失败：常量作为数组长度/类型参数的传播错误；
- `%T` 不符：typed/untyped 常量默认类型规则错误；
- int64/uint64 边界值错误：无类型常量默认类型或溢出处理错误；
- 运行期转换错误：非常量转换的截断语义错误；
- GC 后表变化：初始化期填充的全局数组未被扫描。

## 参数

- `UAF_SEED`：决定并发读取起点与 GC 轮数；断言与起点无关，失败行带出种子。

## 运行

```bash
cd cases/L12_init_link/l12_crosspkg_const_boundary
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
