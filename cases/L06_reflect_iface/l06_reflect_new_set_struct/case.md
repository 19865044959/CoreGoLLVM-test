---
id: l06_reflect_new_set_struct
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/field_test.go:162 reflect.New + 接口断言 + 高级类型 Setter
  - echo@v4.13.4: bind_test.go:442 nil map 初始化与 reflect.MakeMap
  - fiber@v2.52.6: ctx_test.go:4854 反射填充带 slice 字段的 struct
tags: [L06, reflect, reflect.New, Set, MakeSlice, MakeMap, struct-copy, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.New` 创建含标量、指针、嵌套 struct、`[]*node`、`map[string]*node`、二维数组、
接口字段、func 字段与未导出字段的复杂 struct，逐字段 `Set` 构造；再用整体 `Set` 复制另一份
复杂 struct，验证值拷贝与指针字段共享语义。每轮插入显式 GC，比较反射视图与类型化视图。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_reflect_new_set_struct`；
2. `reflect.New(t).Elem()` 可写，所有导出字段按预期赋值，未导出字段 `CanSet()==false`；
3. `MakeSlice/Append/MakeMapWithSize/SetMapIndex` 构造的容器在 GC 后元素与指针字段完整；
4. 整体 `Set(reflect.ValueOf(src))` 复制标量（含未导出字段位）但共享指针字段，修改副本影响 src；
5. `reflect.Zero` 正确将指针字段清为 nil；
6. 五个 profile 下结果一致，无 panic/崩溃。

## 失败判读

- `Set` panic 或 `CanSet` 错误：reflect 可寻址性/只读 flag 错误；
- 整体复制后指针字段不共享：struct 值拷贝语义错误；
- GC 后嵌套指针/slice/map 元素失效：写屏障或堆对象扫描漏标；
- func 字段调用崩溃：闭包环境或 func 值保活失败。

## 参数

- `UAF_SEED`：随机种子，控制字段值、map/slice 插入顺序；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_new_set_struct/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
