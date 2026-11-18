---
id: l12_const_init_expr
dim: L12_init_link
priority: P0
sources:
  - fiber@v2.52.6: middleware/requestid/requestid_test.go:59 具名 ContextKey 类型的 iota 常量
  - grpc-go@v1.71.1: benchmark/primitives/code_string_test.go:31 具名 codeBench 类型的 iota 常量
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:43 常量表达式 `( 1 + 2.0 ) / 3` 的类型提升与求值
  - grpc-go@v1.71.1: security/advancedtls/advancedtls_test.go:51 具名 provType 的 iota 常量
tags: [L12, constants, constant-folding, typed-constants, iota, shifts, default-types, array-length, gc]
requires: []
runtime: "<3s"
---

# 场景

覆盖编译期常量初始化：常量链（`doubled = base*2`）、移位与掩码、`len("hello")`、字符串拼接与 rune→string 转换、float/complex 常量、Go 1.21 的 `min/max` 常量折叠、typed `uint8` 常量与 untyped 常量的默认类型；另用 `iota` 块与依赖 iota 的移位常量。常量同时作为数组长度和 init 表填充公式，验证编译期求值结果与 init 期使用一致。

# 期望行为

1. 常量值：`doubled==14`、`shifted==1024`、`mask==1023`、`helloLen==5`、`tag=="hello!"`、`letterStr=="A"`、`rounded==6.0`、`creal==2`、`cimag==3`、`lo==1`、`hi==3`、`arrLen==16`、`u8max==255`；
2. `modeA/B/C==0/1/2`、`modeShift==16`；
3. untyped 常量的默认类型分别为 `int/float64/rune/string/complex128`，typed 常量保持 `uint8`；
4. 运行时转换 `int8(266)==10`（常量表达式本身合法，转换发生在运行期）；
5. `[arrLen]int` 长度为 16，init 按 `i*14 + 53 + 3` 填充，总和 2576；
6. 常量初始化的字符串表内容正确；
7. 并发只读 + 显式 GC 后常量派生表与求和不变；
8. 最后一行 `ALL PASS l12_const_init_expr`。

# 失败判读

- 常量值错误：常量折叠、溢出或类型转换实现错误；
- 默认类型断言失败：untyped 常量默认类型规则错误；
- 数组长度/字符串表编译失败：常量作为数组长度或常量转换的编译期求值错误；
- init 表值错误：常量在变量/init 期被错误重算；
- GC 后表变化：init 填充的全局数组未被扫描。

# 参数

- `UAF_SEED`：并发 worker 的起始列偏移，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_const_init_expr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
