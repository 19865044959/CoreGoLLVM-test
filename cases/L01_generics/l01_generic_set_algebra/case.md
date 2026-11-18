---
id: l01_generic_set_algebra
dim: L01_generics
priority: P0
sources:
  - go-redis@v9.8.0: doctests/sets_example_test.go:14 ExampleClient_sadd 集合写入与去重
  - go-redis@v9.8.0: doctests/sets_example_test.go:122 ExampleClient_sinter 两个集合取交集 [bike:1]
  - go-redis@v9.8.0: doctests/sets_example_test.go:284 ExampleClient_sdiff 两个集合取差集
tags: [L01, generics, set-algebra, union, intersect, difference, symmetric-difference, ordered-set, pointer-identity, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

自建泛型集合代数：`genSet[T comparable]`（map 实现）提供 add/remove/has/Union/Intersect/Difference/SymDiff/Subset/Equal；`orderedSet[T cmp.Ordered]`（有序切片 + `sort.Search`）提供稳定升序迭代、闭区间 Range 查询与有序并/交。对应 go-redis `SADD/SINTER/SDIFF/SCARD` 的集合语义与 golangci-lint 的启用检查器集合运算。

固定用例复现 redis 文档示例：france={1,2,3}、usa={1,4} 的交集 {1}、差集 {2,3}、并集 4 个、对称差 {2,3,4}；并测试空集、重复 add、删除不存在元素。UAF_SEED 驱动 96 元论域上的 A/B/C 三个随机集合，逐元素与 bool 参考数组比对，并验证交换律、结合律、分配律、De Morgan、吸收律与 A\B ⊆ A。另覆盖有序集合的排序/去重/区间查询、`genSet[*setPayload]` 的指针身份跨 GC、以及结构体 key 实例。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_set_algebra`；
2. 固定示例的交集/差集/并集/对称差元素与基数完全正确，字符串集合结果正确；
3. 随机集合的 Union/Intersect/Difference/SymDiff 对论域每个元素的成员性与参考模型一致；
4. 代数律全部成立：union 交换/结合、交并对分配、De Morgan、吸收律、差集子集关系；
5. `orderedSet[int64]` 严格升序且无重复，Range 查询结果与暴力过滤一致，有序并/交的排序与成员正确；
6. 指针集合删除部分元素后，显式 GC 后成员判断与 payload 内容不变；交集只保留两边共有的指针；
7. 结构体 key 集合的命中/未命中与 GC 后结果正确。

# 失败判读

- `cardinality` / `membership mismatch`：集合运算实现错误（尤其是差集方向或对称差）；
- `De Morgan broken` / `distributive law broken`：操作数顺序或交并实现相反；
- `ordered set not strictly ascending` / `range query`：二分插入位置或上界比较错误；
- `pointer membership mismatch` / `payload corrupted after GC`：map 键中的指针未被正确保活，偏 L18；
- `SIGSEGV` / `fatal error: found bad pointer`：栈映射漏标或写屏障缺失。

# 参数

- `UAF_SEED`：决定三个随机集合的成员构成、有序集合内容与指针集合负载；失败输出带种子；
- 无 TORTURE 参数（P0，单次 <10ms）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_set_algebra/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
