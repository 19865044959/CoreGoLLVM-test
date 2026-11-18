---
id: l09_cgo_struct_fnptr
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 侧算子/回调分发）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 结构体是数据面边界）
tags: [L09, cgo, struct, function-pointer, vtable, layout, dispatch, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库把 vtable 放在结构体里的绑定形态：`op_t{name, fn, arity, code}` 由 `vtable_t{ops[4], n, bias, calls}` 持有，C 侧按索引从结构体字段取函数指针分发，Go 只通过 C 辅助函数读取布局、名称、算子代码并替换方法指针。C 只保存自己的函数指针与内存，不保留任何 Go 指针。

# 期望行为

1. `sizeof(op_t)`、`sizeof(vtable_t)` 与 `offsetof(op_t,fn)`、`offsetof(vtable_t,ops[1])`、`offsetof(vtable_t,bias)`、`offsetof(vtable_t,calls)` 的 Go/C 值一致；
2. 初始 4 个算子的 name/arity/code 正确，非法索引的 name 为 NULL、arity 为 -1；
3. 4 个算子 × 8 组参数（含负数与大数）经结构体内函数指针分发，结果与 Go 参考实现一致；
4. 非法索引返回 `INT64_MIN` 哨兵且不增加调用计数；
5. `bias` 字段只影响返回结果，不影响底层算子；
6. 替换结构体中的函数指针后旧 code 返回、code/唯一指针数及时更新、name 字段不变，新分发结果立即改变；
7. 非法索引/非法 code 的替换不修改任何字段；
8. `vt_copy_name` 在足够、恰好、不足容量与非法索引下的返回值/NUL 终止符合约定；
9. 显式 GC 后 name/code/bias/calls/分发结果全部稳定；
10. 最后一行 `ALL PASS l09_cgo_struct_fnptr`。

# 失败判读

- `sizeof/offsetof`：cgo 对含函数指针字段的结构体布局翻译错误；
- `dispatch ...`：结构体内函数指针读取错位或 C 调用约定错误；
- `invalid ... sentinel`：越界检查缺失或 `INT64_MIN` 常量转换错误；
- `replace ...`：函数指针字段写入错位，或 `fn_code` 指针比较失败；
- `copy_name ...`：长度/容量判断错误；
- GC 后失败：C 错误保存了 Go 指针（本用例只向 C 传 C 堆指针与标量）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，`vtable.c`/`vtable.h` 与 `main.go` 同目录构建。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_struct_fnptr
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
