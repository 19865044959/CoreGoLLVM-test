---
id: l12_iota_consts
dim: L12_init_link
priority: P0
sources:
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:43 整数常量表达式 `(1+2)*3` 求值
  - gitea@v1.23.5: modules/templates/eval/eval_test.go:45 int/float 常量类型提升
tags: [L12, iota, typed-constants, untyped-constants, shifts, constant-folding, gc]
requires: []
runtime: "<3s"
---

# 场景

覆盖编译期常量体系：typed/untyped 常量、iota 枚举与位标志、`1<<(10*iota)`、多常量同一行、空白占位、字符串常量拼接、常量数组长度、默认类型规则与运行期截断。

# 期望行为

1. `weekday` iota 枚举 0..6，switch 命中 Tuesday；
2. `perm` 位标志为 1/2/4，组合为 7，位之间互斥；
3. `KB/MB/GB = 1<<10/1<<20/1<<30`；`P,Q=0,0`、`R,S=1,10`、`first/third=1/3`；
4. `Int62=1<<62`、`U64Shift=1<<63`、`Greeting="go1.24"`、`ExactInt=4`、`untIntDiv=0`、`untFloatDiv=0.5`；
5. 常量可作为数组长度，`ShiftA/B/C=4/8/16`；
6. 无类型常量默认类型依次为 int/float64/string/bool/int32，运行期 `int(1.9)=1`；
7. GC + 垃圾分配后常量与数组内容不变；
8. 最后一行 `ALL PASS l12_iota_consts`。

# 失败判读

- 编译期报常量溢出/类型错误：常量折叠或类型提升实现错误；
- 运行时枚举/位移值错误：编译期常量传播错误；
- 默认类型错误：无类型常量默认类型规则实现错误；
- GC 后数组值变化：常量数据被错误放置到可写/可回收区域。

# 参数

无随机路径；`UAF_SEED` 不参与本用例。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_iota_consts/*.go
GODEBUG=clobberfree=1 /tmp/case
```
