---
id: l06_reflect_array_ops
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: callbacks/create_test.go:16 反射按 Kind 区分 slice/array/struct 并逐元素构造行值
  - fiber@v2.52.6: path_test.go:138 定长 [maxParams]string 参数数组的反射/下标写入
  - caddy@v2.10.0: context_test.go:54 数组形态容器（[]json.RawMessage → []any）的装载
  - pgx@v5.7.4: pgtype/array_test.go:8 PostgreSQL 数组元素/维度解析
tags: [L06, reflect, array, ArrayOf, index, addressability, reflect-copy, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.ArrayOf` 构造 `[8]*item`、二维 `[2][3]int64` 与零长数组，经 `New/Elem/Index/Set`
填充；覆盖数组值装箱后的槽位拷贝语义、指针元素共享、非寻址数组元素不可写、`reflect.Copy`
的数组↔切片路径与元素类型不匹配 panic。每隔关键步骤显式 GC，并仅通过接口中的数组副本与
类型化视图交叉校验。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_array_ops`；
2. `ArrayOf/New/Index/Set` 的元素类型、长度、可写性正确，反射求和与期望一致；
3. 数组值装箱产生独立槽位，但槽位中的指针元素共享；修改指针目标在副本可见；
4. 非寻址数组元素 `CanSet()==false`，直接 `SetInt` 必须 panic 且被 recover；
5. `reflect.Copy` 数组→切片、切片→数组返回正确元素数，元素类型不匹配必须 panic；
6. 多维数组与指针元素在多次 `runtime.GC()` 后仍可逐项解引用；
7. `UAF_SEED` 驱动的随机槽位回写与类型化视图一致。

## 失败判读

- `array pointer element corrupted after GC`：数组指针元素未被扫描，对象被提前回收；
- `array slot copy independent`：数组值装箱被错误实现为引用共享；
- `set non-addressable array element did not panic` / `copy mismatched ... did not panic`：反射语义缺失；
- `nested array survives GC`：多维数组元素位图或偏移错误。

## 参数

- `UAF_SEED`：随机槽位回写与标签值来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_array_ops/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
