---
id: l06_reflect_slice_map
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: callbacks/helper_test.go:11 以反射值/指针为 key 的 visit map 与切片展开
  - gorm@v1.30.0: statement_test.go:11 clone 后 map/slice 各自独立
tags: [L06, reflect, slice, map, pointer-elements, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.MakeSlice/Append/AppendSlice` 构造 `[]*node`，用 `MakeMapWithSize/SetMapIndex/MapKeys/MapIndex` 构造并遍历 `map[string]*node`，再嵌套 `map[string][]*node`。通过反射修改指针指向的字段、删除/插入 key，并在 GC 后与类型化视图互相校验。

# 期望行为

1. 反射 slice 长度 12，索引 3 被改为 999，追加元素 1000/1001 可读；
2. map 删除 k00、插入 k99 后长度 10，k05 的 Label 经反射改为 "changed"；
3. 排序后的 MapKeys 遍历求和与类型化 map 求和一致；
4. 类型化视图写入后反射可读到同一对象，所有指针元素跨 GC 有效；
5. 最后一行 `ALL PASS l06_reflect_slice_map`。

# 失败判读

- `reflected slice after GC` / `map pointer field written via reflect`：反射容器与原变量不再共享底层数据，或元素指针被回收；
- `map size after delete+insert`：`SetMapIndex` 删除语义错误；
- `reflect map iteration sum`：MapKeys/MapIndex 取到错误元素；
- panic：map 元素不可寻址却直接 Set，属于实现 bug。

# 参数

- `UAF_SEED`：map 插入顺序随机化（runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_slice_map/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
