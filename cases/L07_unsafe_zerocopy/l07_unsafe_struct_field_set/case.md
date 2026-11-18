---
id: l07_unsafe_struct_field_set
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 零拷贝重解释为 string
  - goccy-json@v0.10.5: size_test.go:10 断言 64 位结构体字段布局与 unsafe.Sizeof
tags: [L07, unsafe, struct-field, offset, write-barrier, zeroing]
requires: []
runtime: "<10s"
---

# 场景

序列化/池化对象常通过 `unsafe.Add` + 字段偏移直接写入 string、`*node`、`[]byte` 字段，绕过普通赋值路径。用例对顶层字段和嵌套 meta 字段执行写入、覆盖、清零，并验证 string 别名、偏移一致性，以及被替换/清零的旧值在类型化引用下仍可读。

# 期望行为

1. 通过 `unsafe.Add(unsafe.Pointer(r), Offsetof(...))` 写入的 Name、meta.label/code、Link、meta.ref、Data 在 GC 后正确；
2. `meta.label` 由 `unsafe.String` 从 `labelSrc` 构造，字段与源字节保持别名；
3. 旧 `Link`/`ref` 目标经类型化局部变量在 GC 后仍有效；
4. `uintptr(&field)-uintptr(r)` 与 `unsafe.Offsetof` 一致（单表达式算术）；
5. 覆盖 Name/Link 后新值跨 GC 有效，旧 Name 副本与旧节点仍有效；
6. 清零 string/slice/pointer 字段后 GC，全部为 nil/空，旧副本与节点仍有效；
7. 最后一行 `ALL PASS l07_unsafe_struct_field_set`。

# 失败判读

- 字段内容错乱：偏移计算错误（嵌套 meta）或 `unsafe.Add` 起点错误；
- GC 后新指针失效：unsafe 字段写入未触发写屏障（应为类型化指针存储）；
- 清零后旧值不可读：清零误改其他内存，或旧值只被字段引用、缺少类型化保活；
- 仅 stress/k8s 失败：GC=1 下写屏障/栈映射问题。

# 参数

- `UAF_SEED`：决定 name/label/data 的编号与节点值，失败信息带种子；
- `unsafe.Offsetof` 与字段布局相关，本用例按 linux/amd64、linux/arm64 运行。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_unsafe_struct_field_set/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
