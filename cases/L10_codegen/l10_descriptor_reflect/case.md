---
id: l10_descriptor_reflect
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493 反射视图代理具体消息，多轮 GC 后字段仍可读
  - protobuf-go@v1.36.6: internal/reflection_test/reflection_large_opaque_test.go:23 大消息经反射视图读写与具体值一致
  - protobuf-go@v1.36.6: internal/encoding/tag/tag_test.go:18 struct tag 与字段描述符互转
  - ent@v0.14.4: entc/gen/graph_test.go:565 生成前用 reflect.StructTag 校验字段 tag
tags: [L10, L06, descriptor, reflect, struct-tag, dynamic-access, presence, oneof, registry, gc, UAF_SEED]
requires: [reflect]
runtime: "<10s"
---

# 场景

由运行时反射构建生成消息 descriptor 表：解析 `proto:"<number>,<kind>[,opt]"` struct tag，记录字段号、JSON 名、Go 类型、cardinality（标量/optional/repeated/map/message/oneof）与字段索引；按字段号动态 Get/Set，保留 presence、nil 与空容器、typed-nil oneof 语义；descriptor 注册表支持重复检测与冻结；构建期校验重复字段号、未知 kind、错误 tag、未标注字段、非法 map key、非 interface oneof、未知 message 类型。

## 期望行为

1. profile/user descriptor 字段数与字段号排序正确；忽略 `proto:"-"` 与未导出字段；按 number/Go 名/JSON 名均可查；
2. optional 指针 nil 视为 absent，非 nil 返回格式化的值；scalar 始终 present；repeated/map 非 nil 即 present（空容器也 present）；
3. 动态 Set 后具体 struct 字段可见；Get 输出确定：整数十进制、bool true/false、float `g` 格式、字符串 Go 引号、repeated 用 `|` 连接、map 按格式化键排序后用 `,` 连接、message 输出 `bio=` 风格字段；
4. oneof 支持 text/count 分支；typed-nil 包装视为 absent；未知分支/未知字段号返回错误；message 字段 Set 返回不支持错误；消息类型不匹配返回错误；
5. 空 repeated/map 经 Set 后为非 nil 空容器；nil optional/message/oneof 不产生值；
6. 嵌套 message 通过其 descriptor 递归格式化，并受注册表约束（未注册消息类型构建失败）；
7. 注册表重复注册报错，冻结后注册报错但查询仍可用；
8. 构建校验覆盖重复字段号、未知 kind、非法字段号、导出字段缺 tag、map key 类型不匹配、oneof 非 interface、未知 message 类型；
9. 丢弃临时消息/descriptor 变量后多轮 GC，保留的消息仍可按 descriptor 读写，descriptor 表完整；
10. 300 次 `UAF_SEED` 随机动态 Set/Get（含 optional/容器/oneof）与独立模型逐项一致，周期性 GC 后仍一致；
11. 最后一行 `ALL PASS l10_descriptor_reflect`。

## 失败判读

- descriptor 字段数/顺序错：未过滤 `-`/未导出字段或未按字段号排序；
- optional 零值被当作 present：缺少指针 nil 判断；
- 空容器被判 absent：用 `len==0` 而非 `IsNil`；
- typed-nil oneof 返回分支：interface 判断未检查包装指针是否为 nil；
- 动态 Set 不可见：reflect.Value 不可寻址或未对 Elem 操作；
- 错误 tag 未拦截：tag 解析未校验字段号/kind/类型匹配；
- GC 后崩溃/值丢失：descriptor 或消息未被保留，或误存了 reflect.Value 而非 Type；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机读写序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_descriptor_reflect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
