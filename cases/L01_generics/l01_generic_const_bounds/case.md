---
id: l01_generic_const_bounds
dim: L01_generics
priority: P0
sources:
  - go-redis@v9.8.0: internal/hscan/hscan_test.go:58 字段扫描的数值溢出边界与错误先于写入
  - pgx@v5.7.4: pgtype/range_codec_test.go:19 Range[Int4] 空区间/闭开区间/零值/nil 的编解码边界
  - ent@v0.14.4: schema/field/field_test.go:941 Type 枚举越界值（0/21）返回 invalid 的边界断言
tags: [L01, generics, type-constraint, numeric-boundary, overflow-wrap, custom-underlying-type, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

用 `Signed`/`Unsigned`/`Integer`/`Ordered` 约束覆盖 `~int8/~uint8/~int16/~int64/
~uint64/~float64/~string` 与自定义底层类型 `celsius(int16)`、`level(uint8)`；
在 `math.MinInt8/MaxInt8`、`MaxUint8`、`MaxInt16`、`MaxInt64/MaxUint64`、正负零、
NaN、空串与 `"\xff"` 上验证 clamp/minmax/泛型加减回绕/`slices.Sort`/
`slices.BinarySearch` 的边界语义。对应 go-redis 数值溢出、pgx 区间空值边界。

# 期望行为

1. `clamp` 在 lo/hi/零值/全范围与 NaN、正负无穷上的返回值符合比较语义，NaN 原样通过；
2. `minmax` 对极值、空串、最大字节串、正负零返回正确的 min/max（含 -0 符号位）；
3. `addWrap`/`sumWrap` 在 int8、uint8、int64、uint64 最大值处按补码回绕，零值/空切片返回 0；
4. 自定义底层类型经 `~` 约束实例化，边界回绕与 clamp 结果一致；
5. stdlib 泛型排序/二分在 int8/uint8/uint64 极值上保持有序且命中/未命中正确；
6. 边界值指针在 GC 后内容不变；
7. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_const_bounds`。

# 失败判读

- `int8 max+1 must wrap to min` 等：约束实例化后的算术被提升/截断到错误宽度；
- `NaN must pass through clamp`：比较被错误常量折叠或改写；
- `custom type ...`：`~` 底层类型实例化布局或方法集错误；
- `... pointer corrupted after GC`：边界值指针未被栈/堆扫描；
- `FAILED ... seed=`：种子仅改变重复次数，失败时随输出给出。

# 参数

- `UAF_SEED`：决定边界表重复次数（50..59），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_const_bounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
