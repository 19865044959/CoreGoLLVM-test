---
id: l01_generic_json_envelope
dim: L01_generics
priority: P0
sources:
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/marshaler_test.go:20 Marshaler 接口 + typed-nil 判空后编码
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/struct_test.go:15 struct tag 编解码、嵌套 struct、omitempty/null 语义
  - sqlc@v1.28.0: internal/config/config_test.go:26 严格 JSON 解码：缺字段/未知字段/未知版本均报错
tags: [L01, generics, json, serialization, nil-vs-empty, custom-marshaler, strict-decode, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

以泛型 `Envelope[T]`（Kind/Seq/Data *T/Extra/Raw）覆盖 `T=item`、`[]string`、
`map[string]int`、`int64` 的 JSON 往返；以泛型 `Pair[T]` 实现自定义 `MarshalJSON`；
用 `json.Decoder.DisallowUnknownFields` 做严格解码错误路径。重点区分 nil Data、
指向空切片的 Data、空 map 与零值，并保留 `json.RawMessage`。对应 ent 的
Marshaler/Unmarshaler 与 omitempty/null、sqlc 的严格配置解码。

# 期望行为

1. golden 输出固定：结构体字段顺序 + map 键排序得到确定 JSON（含 extra/raw）；
2. 往返后 Kind/Seq/Data/Extra/Raw 全部一致，payload 指针字段正确；
3. 零值 Envelope 输出 `{"kind":"","seq":0,"data":null}`；空切片输出 `[]`、nil 输出 `null`，往返保持非 nil/ nil 区别；空 map 输出 `{}` 并往返为非 nil 空 map；
4. `Pair[item]` 的泛型 Marshaler 输出固定 golden，默认 Unmarshal 可回读；
5. 未知顶层字段、payload 类型错误、坏 JSON 均返回 error 且不 panic；top-level `null` 解码为零值；
6. 非法 RawMessage 在 Marshal 时报错；
7. 批量往返后的 payload 在 GC 后仍完整；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_json_envelope`。

# 失败判读

- `envelope golden mismatch`：字段顺序/omitempty/自定义编码器行为变化；
- `empty slice did not roundtrip as non-nil empty` / `null data ...`：nil 与空被合并；
- `generic pair golden mismatch`：泛型类型上方法集/接收者实例化错误；
- `strict decode accepted ...` / `malformed JSON must return error`：解码错误被吞掉；
- `decoded payload corrupted after GC`：解码对象指针未被扫描；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定批量往返条数（14..22），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_json_envelope/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
