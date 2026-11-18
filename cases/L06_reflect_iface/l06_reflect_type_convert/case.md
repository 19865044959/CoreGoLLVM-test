---
id: l06_reflect_type_convert
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/field_test.go:16 reflect.Value.Set/Convert 写入 string/int/time/alias 字段
  - gorm@v1.30.0: schema/field_test.go:305 alias 与 defined type 的反射判定差异
  - pgx@v5.7.4: pgtype/pgtype_test.go:527 类型化 encode plan 缓存串型后的回退路径
tags: [L06, reflect, Convert, CanConvert, named-type, slice-to-array, interface-conversion, panic, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 28 项表驱动矩阵覆盖 `reflect.Value.Convert/CanConvert`：整数截断与回绕、`int64→string` rune
转换、float 截断、string↔[]byte/[]rune、命名类型与底层类型互转、`[]byte↔Raw`、slice→array 与
slice→array 指针、命名指针转换、忽略 tag 的 struct 转换、chan 方向转换、具体类型→接口；以及
string→int/array→slice 等 7 项不可转换路径。覆盖短切片转数组、零 Value 转换的 panic，GC 后
校验转换结果的指针别名与内容。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_type_convert`；
2. 21 项可转换与 7 项不可转换的 `ConvertibleTo/CanConvert` 判定全部符合预期；
3. 转换结果类型、值（回绕/截断/rune/命名类型/struct 字段）全部正确；
4. 具体类型→接口的 Convert 成功且可断言回原接口；
5. 短切片→数组、零 Value、不可转换类型均 panic 并被 recover；
6. slice→array 指针与命名指针转换共享底层对象，写一处另一处可见；
7. 转换得到的 []byte/[]rune 仅由全局槽保活，GC 后内容与指针别名仍正确。

## 失败判读

- `ConvertibleTo ...` / `CanConvert ...`：类型转换规则实现错误；
- `converted value ...`：转换语义错误（如 int→string、宽度截断、struct tag 处理）；
- `slice to array pointer aliases backing array`：slice→array 指针未共享底层数组；
- `... did not panic`：不可转换路径未触发 panic，可能静默产生错误值。

## 参数

- `UAF_SEED`：基础整数取值（回绕/截断期望随种子变化）；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_type_convert/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
