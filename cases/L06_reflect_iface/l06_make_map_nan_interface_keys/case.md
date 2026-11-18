---
id: l06_make_map_nan_interface_keys
dim: L06_reflect_iface
priority: P1
sources:
  - pgx@v5.7.4: pgtype/numeric_test.go:105 NaN 值转换到 float64 目标
  - echo@v4.13.4: context_test.go:547 map[string]float64 中写入 math.NaN 后序列化
  - pgx@v5.7.4: pgtype/hstore_test.go:266 map 容器文本/二进制往返
  - gin@v1.10.0: binding/form_mapping_test.go:222 reflect.MakeSlice 构造目标切片
tags: [L06, reflect, MakeMap, MakeSlice, NaN-key, interface-key, typed-nil, append, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

指标/数值解码会把 NaN 与 ±0 写入动态构造的 map 与 slice：NaN 不等于自身，因此每次插入都是
新条目、查找恒失败、Delete 无效；`+0` 与 `-0` 却是同一 key。接口 key 还需按动态类型区分。
本用例用 `reflect.MakeMapWithSize/MakeSlice/Append/SetMapIndex` 构造容器，覆盖 NaN、±0、
接口 key、typed nil 指针 key、切片元素作 key、零尺寸元素切片与错误 key 类型 panic，并跨 GC
校验所有条目与指针值。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_make_map_nan_interface_keys`；
2. float64 key 的 map 中插入 3 个 NaN 得到 3 个独立条目；用 NaN 查找恒返回无效 Value，
   用 NaN 删除不改变 Len；`+0`/`-0` 是同一 key，后插入的值覆盖前者；
3. `MapKeys` 返回全部 NaN 条目；`MapRange` 计数与求和与插入顺序无关；
4. 接口 key map 区分 `int64`/`int`/`string`/命名 int 的 `1`，两个 NaN 独立，typed nil 指针
   是相等 key（重复插入不增 Len）；NaN 接口 key 查找失败，typed nil key 可查；
5. slice 元素作 map key 时 NaN 独立、±0 合并；`Append` 超 cap 后 NaN 与 `-0` 值保持；
6. 零尺寸元素 `MakeSlice(0,1024)` 合法，cap 内 Append 不增长；错误 key 类型 `MapIndex` panic；
7. 容器只由反射 `Value` 槽保活时，GC 后所有条目、指针值、NaN/-0 位模式仍正确。

# 失败判读

- `each NaN key ...` / `NaN key lookup` / `deleting NaN`：float key 的哈希/相等实现错误；
- `+0 and -0`：零值规范化错误；
- `interface keys distinguish ...`：接口 key 的动态类型未参与哈希/相等；
- `slice element ... key`：切片元素到 map key 的转换或 NaN 语义错误；
- `zero-size element slice` / `Append grows`：MakeSlice/Append 边界错误；
- `... after GC`：map/slice 中的指针或 NaN 值未被保留；
- `... did not panic`：反射 key 类型校验缺失。

# 参数

- `UAF_SEED`：每轮节点值与标签基值来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_make_map_nan_interface_keys/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
