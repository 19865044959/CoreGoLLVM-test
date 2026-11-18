---
id: l12_crosspkg_const_chain
dim: L12_init_link
priority: P0
sources:
  - grpc-go@v1.71.1: codes/codes_test.go:51 want := []Code{OK, NotFound, Internal, Canceled} 使用跨包导入的类型化常量
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:43 常量表达式 `(1+2.0)/3` 的类型提升与求值
  - fiber@v2.52.6: middleware/requestid/requestid_test.go:59 `const requestContextKey ContextKey = iota` 类型化 iota 常量
tags: [L12, module, constants, const-chain, cross-package, typed-constants, iota, array-length, constant-folding, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

`unit` 子包导出 typed/untyped 常量（iota 枚举、移位链、位组合、`Priority` 链），`shape` 子包用这些常量作为 const 表达式结果与数组长度（`[unit.MaxLen]string` 等），main 再用两个子包的常量计算 `TotalLen` 并定义 `[TotalLen]int64` 表，在 init 中按常量公式填表。用例验证跨包常量传播、类型保持、默认类型与运行期截断。

## 期望行为

1. `unit.Shift/Scale/Mask/MaxLen/Combined/KindCount/Weighted` 分别为 4/16/15/48/33/3/20；
2. `%T`：typed 常量为 `unit.Kind`，untyped `Shift` 为 `int`，`Weighted` 为 `unit.Priority`；
3. `shape.ShapeScale/ShapeArea/ShapeMask` 为 32/96/15，`shape.KindOfShape==unit.KindLarge`；main 的 `TotalLen==144`、`Derived==291`；
4. `len(shape.Labels)==48`、`len(shape.Defaults)==48`、`len(table)==144`（数组长度来自跨包常量）；
5. `Labels[i]=="k%02d"`、`Defaults[i]==i*16+32`、`table[i]==i%15+16`；`shape.InitDefaultsOK` 为真；
6. 以 typed 常量作 map key 的三项查找与 switch 分支正确；常量位表达式 `Combined&0x3` 命中 `KindSmall`；
7. 运行期转换 `unit.Kind(300)==44`、`unit.Priority(70000)==4464`（常量转换会编译失败，故用变量）；
8. 8 goroutine 并发读常量派生表 + 显式 GC 后内容不变；
9. 最后一行 `ALL PASS l12_crosspkg_const_chain`。

## 失败判读

- 常量值/类型错误：跨 package 常量传播、常量折叠或类型保持错误；
- `%T` 不符：typed/untyped 常量默认类型规则错误；
- 数组长度编译失败或运行期长度错误：常量作为数组长度的传播错误；
- init 填表值错误：常量公式在初始化期被错误重算；
- 运行期截断值错误：非常量转换语义错误；
- GC 后表变化：初始化期填充的全局数组未被扫描。

## 参数

- `UAF_SEED`：决定并发读取起点；断言与起点无关，失败行带出种子。

## 运行

```bash
cd cases/L12_init_link/l12_crosspkg_const_chain
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
