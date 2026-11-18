---
id: l06_makeslice_makemap_bounds
dim: L06_reflect_iface
priority: P2
sources:
  - gin@v1.10.0: binding/form_mapping_test.go:222 reflect.MakeSlice + 逐值 ParseInt
  - echo@v4.13.4: bind_test.go:442 nil map 需初始化（reflect.MakeMap 分支）
  - pgx@v5.7.4: internal/pgio/write_test.go:23 预分配 cap 后 append 的容量语义
  - gorm@v1.30.0: callbacks/create_test.go:16 slice/array 行值构造
tags: [L06, reflect, MakeSlice, MakeMap, SetLen, SetCap, append-aliasing, nil-map, panic-boundary, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

反射构造批量结果时大量使用 `MakeSlice`/`MakeMap`/`Append`/`SetMapIndex`；容量、可寻址性、
共享底层数组和 nil map 语义错误会造成静默数据损坏。本用例系统覆盖 MakeSlice 零值/窗口/
SetLen/SetCap、Append 在 cap 内共享与超 cap 复制、Copy 重叠、接口/结构体切片、MakeMap
hint/删除/错误类型/nil map panic，以及 MapOf(SliceOf) 嵌套动态类型跨 GC。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_makeslice_makemap_bounds`；
2. `MakeSlice(len,cap)` 的 Len/Cap/零值/Slice/Slice3 正确；负 len、len>cap、越界 Slice3 panic；
3. MakeSlice 返回值不可寻址，SetLen/SetCap 需可寻址切片；SetLen 补零、越 cap panic，
   SetCap 可缩小、低于 len/高于 cap panic；
4. Append 在 cap 内共享底层数组（写一端可见于另一端），超 cap 后复制；错误元素类型 panic；
   Copy 全量/重叠/string→[]byte 语义正确；
5. 接口切片零元素为 nil 接口，结构体切片指针字段跨 GC 完整；
6. MakeMap 空 map 非 nil；SetMapIndex 插入/删除（零 Value）/错 key/错 value 符合预期；
   负 hint 返回空 map；非 map 类型与 nil map 写入 panic；
7. `MapOf(SliceOf(int))` 嵌套 map 在 GC 后仍可读；默认 16 轮 ×64 元素在 60s 内完成，
   `TORTURE_ROUNDS`/`TORTURE_ITEMS` 可放大。

## 失败判读

- `MakeSlice len cap` / `zeroed` / `Slice window`：分配或切片窗口错误；
- `... did not panic`：边界检查缺失或错误地 panic；
- `Append within cap shares backing array` / `Append beyond cap copies`：扩容/别名语义错误；
- `SetMapIndex ...` / `nil map`：map 写入与 nil 语义错误；
- `... after GC`：切片/map 中指针或嵌套容器未被扫描。

## 参数

- `UAF_SEED`：循环中写入的值来源；失败输出带出种子；
- `TORTURE_ROUNDS`：轮数（默认 16，上限 2000）；
- `TORTURE_ITEMS`：每轮 element 数（默认 64，上限 20000，最小 8）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_makeslice_makemap_bounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=500 TORTURE_ITEMS=8192 UAF_SEED=7 /tmp/case
```
