---
id: l10_defaults_alias_copy
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/encoding/defval/default_test.go:17 默认值按 kind 格式化
  - protobuf-go@v1.36.6: proto/equal_test.go:464 显式设置默认值与未设置值的相等性区分
  - protobuf-go@v1.36.6: proto/reset_test.go:15 Reset 后生成消息复用
  - protobuf-go@v1.36.6: proto/decode_test.go:123 零长度 bytes 解码为非 nil 空切片
  - ent@v0.14.4: schema/field/field_test.go:44 Default+Min/Max 生成的默认值/校验描述
tags: [L10, L06, defaults, presence, copy-on-read, copy-on-set, frozen-table, merge, reset, gc, UAF_SEED]
requires: []
runtime: "<10s（UAF_SEED 驱动随机 presence 组合）"
---

# 场景

生成式默认值表：7 个字段（int32/float64/string/bytes/[]string/map/enum）从紧凑 spec 构建并冻结。消息用 presence 位区分“未设置”与“显式零值”：未设置时 getter 返回默认值，可变默认（bytes/slice/map）每次读取返回新副本，调用方修改不会污染表或其它消息；显式设置的零值/nil/空容器优先于默认值。setter 对调用方容器 copy-on-write；present 容器 getter 返回消息内部视图（与 absent 的复制语义形成边界）。merge 仅覆盖源 present 字段并深拷贝，reset 恢复默认。

## 期望行为

1. 零值消息所有 Has=false，标量/枚举/容器 getter 返回约定默认值；
2. bytes/tags/limits 默认值每次读取独立，修改返回值后再次读取与表内容均不变；
3. 两个消息之间不共享可变默认副本；
4. 显式 `0`、`""`、`nil`、空容器覆盖默认值且 presence 为 true；`nil` 与空非 nil 可区分；
5. setter 复制输入容器，调用方事后修改不影响消息；present getter 返回活视图，修改可见；
6. merge：源 present 覆盖、源 absent 保留目标、容器深拷贝；nil 源 no-op；
7. reset 后恢复默认值，reset 前取得的快照在 GC 后仍有效；
8. 15 类 spec 构建错误（重复字段/名、越界、坏数字、NaN/Inf、坏 hex、坏 map、未知枚举、未知 kind 等）全部拒绝；
9. 空默认（`-`）的 bytes/slice/map 保持非 nil 空；
10. 300 个消息与 `UAF_SEED` 随机 presence/值组合在 GC 后 getter/隔离语义仍成立。

## 失败判读

- `FAIL: ...`：默认值解析、presence 或复制语义错误；
- `default copy isolated` / `table ... unchanged` 失败：可变默认被直接暴露，存在别名污染；
- `setter copies input containers` 失败：setter 保留了调用方引用；
- `merge deep-copies containers` 失败：合并后源修改影响目标；
- `SIGSEGV` / `found bad pointer`：冻结表或 clone 后的容器指针失效。

## 参数

- `UAF_SEED`：随机 presence/取值组合种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_defaults_alias_copy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
