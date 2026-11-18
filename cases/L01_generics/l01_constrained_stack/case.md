---
id: l01_constrained_stack
dim: L01_generics
priority: P0
sources:
  - protobuf-go@v1.36.6: internal/set/ints_test.go:24 泛型 Set[T ~uint32|~uint64] 的位图边界与 Set/Has/Clear
  - pgx@v5.7.4: pgtype/range_codec_test.go:19 Range[pgtype.Int4] 等以数值类型参数承载边界的泛型实例
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/stack_test.go:26 NewStack[int] 交替 Pop/PopBottom 消费
tags: [L01, generics, constrained-stack, numeric-constraint, running-min-max, pointer-payload, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

带数值约束的泛型栈 `NumStack[T numeric]`，约束为 `~int64 | ~int32 | ~float64`；元素为泛型 `cell[T]` 的指针（值 + blob），栈同时维护与元素等长的运行最小值/最大值数组和总和，支持 O(1) `Min/Max/Sum`。对应泛型数值集合/区间边界实例化与泛型栈容器两类真实用法。

int64 实例压入 180–219 个含负数与重复极值的元素，中途与压栈后显式 GC，逐项校验指针身份与独立复算的 Min/Max/Sum；随后弹出后半段并在弹出过程中穿插 GC，每次复算聚合值；清空后验证 Min/Max 不可用、Sum 归零。再以 int32 与 float64 实例化，验证同一约束下不同底层类型。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_constrained_stack`；
2. 压栈后 Len 正确，Sum/Min/Max 与逐元素复算完全一致；
3. 每个栈元素指针与创建时相同，id/blob 在 GC 后不变；
4. 弹出返回的指针与压入顺序严格对应，弹出后 Min/Max/Sum 同步更新；
5. 空栈 Min/Max 返回 false，Sum 为零值；int32/float64 实例聚合值正确且排空后 Sum 归零；
6. 在 clobberfree 等 profile 下结果一致。

# 失败判读

- `min/max/sum ... mismatch`：运行极值/总和维护错误，优先检查 Push/Pop 的并行数组同步；
- `stack item ... pointer identity changed`：泛型负载指针跨 GC 失效；
- `empty stack exposed min/max`：空栈状态判断错误；
- 超时：正常无并发，应秒级完成，超时多为 GC 饥饿。

## 参数

- `UAF_SEED`：决定 int64 元素个数（180–219），值域固定。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_constrained_stack/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
