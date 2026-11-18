---
id: l17_slice_struct_byval
dim: L17_stackmap
priority: P1
sources:
  - goccy-json@v0.10.5: decode_test.go:2947 TestDecodeSlice（含 *B / []*A 指针字段的聚合类型）
tags: [L17, stackmap, byval, aggregate, slice-of-struct, string]
requires: []
runtime: "<5s"
---

# 场景

把 goccy/go-json `TestDecodeSlice` 中含指针字段的嵌套聚合（`A{B *B}`、`X{A []*A}`）抽象为 stdlib 记录：`record` 同时含 `*item`、`[]*item`、`[2]*item`、`[]string`、`[]byte`。用例以三种方式规模化“按值传递”：

1. `record` 按值传参并 GC；
2. `[]record` 按值传参、遍历每个元素时 GC；
3. `[3]record` 按值传参，以及 noinline 函数按值返回 `[3]record` / `[]record`。

按值复制会把指针/字符串/切片头复制到被调栈帧；若聚合参数的栈映射漏标，GC 会回收仍被副本引用的底层对象。

# 期望行为

1. `sumByVal`/`sumSliceByVal`/`sumArrayByVal` 返回的指针值和等于期望值；
2. 每次 GC 后，原聚合、切片元素、数组元素及 noinline 返回值中的全部指针字段、字符串内容、`data` 字节均正确；
3. 按值传参不破坏调用方原聚合；
4. 16 轮稳定，退出码 0，最后一行 `ALL PASS l17_slice_struct_byval`。

# 失败判读

- `FAIL: *aggregate* corrupted` / `retSlice[*]` / `retArray[*]`：对应聚合副本或返回值的栈槽漏标；
- `byval * sum` 不为期望值：被调函数内聚合参数指针失效；
- clobberfree 下 `data` 首字节变垃圾：`[]byte` 头未被扫描；
- SIGSEGV：漏标后的 UAF。

# 参数

无随机路径；`UAF_SEED` 不影响结果。每轮含多次 `runtime.GC()`，固定 16 轮。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_slice_struct_byval/*.go
GODEBUG=clobberfree=1 /tmp/case
```
