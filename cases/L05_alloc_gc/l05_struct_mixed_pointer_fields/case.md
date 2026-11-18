---
id: l05_struct_mixed_pointer_fields
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: issue_test/issue100_test.go:30 的 TestLargeMapValue：含 Pad/[160]byte 与 string 的大 struct
  - goccy/go-json@v0.10.5: size_test.go:10 的 TestOpcodeSize：依赖结构体字段布局与尺寸断言
  - fasthttp@v1.59.0: http_test.go:117 的 TestRequestCopyTo：含 header/body 子切片的深拷贝
tags: [L05, struct-layout, mixed-pointers, pointer-bitmap, zeroing, gc]
requires: []
runtime: "<10s"
---

# 场景

2500 个混合字段 struct 组成的大数组：每元素同时含标量、float、`*node`、
string、[]byte、map、interface{}、func 值、uintptr 与定长数组。数组在堆上被
精确扫描；随后把每 4 个元素的指针字段全部置 nil，验证 GC 位图不会把标量/
uintptr 误当指针，也不会漏扫 func/interface 环境。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_struct_mixed_pointer_fields`；
2. 填表并 GC 后，2500 个元素的全部字段与种子推导值一致；
3. typed-nil `*node` 放进 interface{} 后接口非 nil，type switch 结果正确；
4. 每 4 个元素清空 p/buf/tbl/any/fn 后，GC 不改变其它元素，被清空字段保持 nil；
5. 仅保留归一化子切片并丢弃整个大数组后，GC 仍能通过子切片保活旧元素；
6. 复制出的 pin 元素跨大块垃圾 + 两次 GC 后完整；
7. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `element ... corrupted after fill+GC`：结构体位图漏标某个指针字段；
- `after nil phase`：已清空字段被 GC 重新写回，或标量/uintptr 被误认为指针；
- `kept ... after array drop`：子切片未保活底层数组；
- `fatal error: found bad pointer`：字段对齐/指针位图错误。

## 参数

- `UAF_SEED`：决定 buf 长度/内容、标量偏移与闭包捕获值；失败输出带种子；
- 无其他 TORTURE 参数；规模固定 2500。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_struct_mixed_pointer_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
