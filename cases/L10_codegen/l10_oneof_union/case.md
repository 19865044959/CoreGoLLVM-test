---
id: l10_oneof_union
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:25 Open/hybrid 生成消息的 oneof Get/Has/Clear/Set 方法集
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:150 Opaque 生成消息的 oneof 访问器与零值语义
tags: [L10, L06, oneof, type-switch, typed-nil, last-one-wins, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 protoc-gen-go 的 oneof 生成形态：`Which isChoice` 接口 + 四个包装 struct（text/num/blob/node），Get/Has/Clear 与 `WhichOneof` 用类型 switch 分派；编码按 oneof 实际类型选字段号，解码遇同组多字段时 last-one-wins。覆盖未设置、typed-nil 包装、空 blob presence、畸形输入以及 GC 前后的载荷存活。

## 期望行为

1. 每个分支的 Get 只返回本分支值，其余 Get 返回零值；`HasChoice`/`WhichOneof` 与设置一致；
2. 四类分支 marshal→GC→unmarshal 后逐分支相等（含 node 的 Label/Data、blob 字节）；
3. 未设置与 `ClearChoice` 编码为空，typed-nil 包装视为未设置且编码为空、Get 不 panic；
4. 同一 oneof 字段连续出现时后值生效；
5. 空（非 nil）blob 解码后 presence 仍为 blob，长度为 0；
6. 截断、错误 wire、未知字段号返回错误；
7. 800 条 `UAF_SEED` 随机分支 roundtrip 相等，GC 后保留对象的 node/blob 载荷可读；
8. 最后一行 `ALL PASS l10_oneof_union`。

## 失败判读

- 分支 Get 串值：类型 switch case 顺序或接口装箱错误；
- typed-nil panic：Get/Has 未判空包装指针；
- GC 后读取乱码/崩溃：oneof 载荷切片未保活（UAF）；
- last-one-wins 失败：解码未覆盖前值；
- 空 blob 变 nil：解码按 `append(nil, ...)` 路径丢失非 nil 空切片语义。

## 参数

- `UAF_SEED`：随机分支/载荷种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_oneof_union/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
