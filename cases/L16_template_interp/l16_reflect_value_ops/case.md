---
id: l16_reflect_value_ops
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: internal/unsafe2/unsafe_test.go:10 reflect.StructOf 构造动态结构体并观察字段布局
  - yaegi@v0.16.1: interp/interp_eval_test.go:732 解释器动态类型在 reflect.TypeOf 下的观测
  - hugo@v0.155.0: tpl/collections/where_test.go:683 reflect.Value 比较与子元素求值
  - hugo@v0.155.0: common/hreflect/helpers_test.go:113 Indirect 处理双指针/接口/带方法结构体
tags: [L16, reflect, deep-copy, map-range, slice-ops, struct-of, convert, comparable, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect` 对带环指针图做深拷贝（保留共享指针与环、接口字段），对 map 使用 `MapRange`/`SetMapIndex`，对 slice 使用 `Index/SetInt/Slice/Slice3/Swapper`，并验证 `Convert`、`OverflowInt`、`Comparable`、`Equal` 的语义与 panic 边界；最后用 `reflect.StructOf` 创建带 tag 的动态结构体并读写字段。对应 yaegi 的动态类型/StructOf 与 Hugo 的 reflect.Value 比较场景。

# 期望行为

1. 深拷贝 root（`root->a->root` 环、Kids 共享 a、Extra 指向 b）后：拷贝根/Next 均为新对象，环保留为 `copy.Next.Next == copy`，`Kids[0] == Next` 共享关系保留，`Extra` 指向拷贝后的 b；修改拷贝的 Tags/Value/Kids 不影响原图；
2. 反射读取 `Value` 字段为 10 并 `SetInt(11)` 生效；`Next.Name` 链式字段为 `a`；`Kids[0]` 指针与 `Next` 相同；缺失字段返回无效 Value；
3. map 键排序为 `[a b nil]`，值指针正确；nil map 的 `MapRange` 为空；`SetMapIndex` 插入/删除生效；
4. `Index(0).SetInt` 与 `Swapper` 修改底层数组，`Slice(1,3)` 长度 2、`Slice3(1,4,5)` len/cap 为 3/4；
5. 3.9→int 得 3，int64(1<<40)→int32 截断，[]byte↔string 转换与 UTF-8 字节长度正确；int→float 可转换，[]int→string 不可转换；int8 的 OverflowInt 边界正确；string 的 Equal 为真/假，slice 的 Equal 必须 panic；含 map/slice 的 struct `Comparable=false`；
6. `StructOf(ID int64 json:"id"; Label string; Next *node)` 字段数/偏移递增、tag 正确，动态值写入 7/"dyn"/a 后 roundtrip 仍可读；
7. 三轮 GC 后拷贝环、共享指针、原图、map 与反射字段读取均稳定；
8. 末行输出 `ALL PASS l16_reflect_value_ops`。

# 失败判读

- `FAIL: cycle must be preserved` / `shared pointer ...`：深拷贝 seen 表或指针处理错误；
- `FAIL: original mutated by deep copy`：map/slice 引用被误共享；
- `FAIL: Convert/Overflow/Comparable`：反射转换语义异常；
- `FAIL: dynamic ...`：StructOf 运行时类型布局/tag 错误；
- GC 轮次失败：反射值/指针图跨 GC 丢失。

# 参数

- `UAF_SEED`：决定 GC 轮次 map 抽查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_value_ops/*.go
UAF_SEED=5 GODEBUG=clobberfree=1 /tmp/case
```
