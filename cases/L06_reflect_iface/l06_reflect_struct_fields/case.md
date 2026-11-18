---
id: l06_reflect_struct_fields
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/field_test.go:16 反射 Set/Convert 写 string/int/float/bool/别名类型字段
  - gorm@v1.30.0: schema/field_test.go:92 反射写指针字段与 nil 清零语义
tags: [L06, reflect, struct-field, pointer-field, Set, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.Value.FieldByName` 读写 struct 的标量、命名类型、指针、嵌套 struct、`[]*struct` 字段；用 `reflect.New` 整体构造后装箱成接口，再用 `reflect.Zero` 验证指针字段 nil 语义。每次写读之间插入 GC，校验 `*node`、`*int64` 与 slice 元素在 GC 后仍有效。

# 期望行为

1. 经反射写入后，类型化视图读到相同值（ID/Name/Ratio/OK/Count/Alias）；
2. 指针字段 Tag、Ptr、嵌套 Inner.Tag、Items 元素在 GC 后解引用正确；
3. `reflect.Zero` 清空指针字段得到 nil，恢复后跨 GC 仍可解引用；
4. 未导出字段 `CanSet=false`、`CanInterface=false`；
5. 反射构造的 struct 装箱进 interface 后跨 GC 取值正确；
6. 最后一行 `ALL PASS l06_reflect_struct_fields`。

# 失败判读

- `reflect read ...` / `Tag pointer survives GC`：反射写入未落盘或指针字段被回收，检查字段偏移/类型元数据；
- `unexported field not settable`：反射权限判定错误；
- `boxed reflected struct`：接口数据区未扫描 struct 内指针；
- panic：`Set` 目标不可寻址或类型不匹配，通常是用例/编译器类型信息问题。

# 参数

- `UAF_SEED`：与轮次组合决定字段基准值（runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_struct_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
