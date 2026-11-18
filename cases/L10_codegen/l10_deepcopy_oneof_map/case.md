---
id: l10_deepcopy_oneof_map
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/methods_test.go:172 TestLegacyMergeMethod 深拷贝合并且源对象不被修改
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:25 TestOpenGet oneof Get/Has 与 typed-nil 包装
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:150 TestOpaqueGet opaque oneof 访问器与零值语义
  - protobuf-go@v1.36.6: proto/merge_test.go:390 merge map fields 的 map 深拷贝语义
  - ent@v0.14.4: dialect/sql/scan_test.go:252 指针切片逐元素分配
tags: [L10, deepcopy, oneof, map-bytes, reuse-destination, nil-semantics, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

生成风格 `DeepCopyInto`/`DeepCopy` 变体：消息包含 oneof（text/blob/child/list，含 typed-nil）、`map[string][]byte`（值可能是 nil/空/非空）、指针切片（含 nil 元素）、optional int32 与字符串。额外验证 `DeepCopyInto` 复用已脏目标对象时覆盖而非合并，旧目标载荷不再被引用。

## 期望行为

1. 固定消息复制后 dump 相等、无共享底层内存；共享源 child 被各自复制，nil child 保留，空 map value 保持非 nil 空，nil map value 保持 nil；
2. optional 显式零值（指针非 nil、值 0）复制后仍有 presence 且指针独立；nil optional 保持 nil；
3. typed-nil oneof 包装保留为 typed nil，Get/WhichChoice/HasChoice 不 panic 且行为符合生成器访问器；
4. 复用脏目标 `DeepCopyInto` 后与源 dump 相等：map/slice 被替换而非追加，旧 child 指针与旧 oneof 缓冲不再被引用，旧缓冲后续修改不影响结果；
5. 源/副本双向修改隔离（oneof blob/list、map value、child tags/data、optional、items）；
6. 源释放后副本、optional 指针、oneof blob、map value、child 视图经多轮 GC 仍可读；
7. 160 个 `UAF_SEED` 随机消息满足：复制相等、无共享引用、源修改隔离、二次拷贝稳定、周期性 GC 存活、脏目标复用正确；
8. 最后一行 `ALL PASS l10_deepcopy_oneof_map`。

## 失败判读

- oneof 分支串值：类型 switch 顺序或接口装箱错误；
- typed-nil panic：Get/Has 未判空包装指针；
- map value 共享：只复制 map 头未逐 value 复制 `[]byte`；
- 旧目标残留：`DeepCopyInto` 追加而非覆盖，或未清理 map；
- 空容器变 nil：复制判断用了 `len(x)`；
- GC 后乱码/崩溃：副本未持有深层对象；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机 oneof/map/items/presence 组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_deepcopy_oneof_map/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
