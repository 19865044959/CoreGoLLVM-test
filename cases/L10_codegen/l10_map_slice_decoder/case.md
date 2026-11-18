---
id: l10_map_slice_decoder
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:871 repeated scalars 逐元素解码
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:966/991 map 字段键值解码与类型转换
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:1058 重复 map 键 last-wins
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:861 repeated 字段为 null 时保持未设置
  - protobuf-go@v1.36.6: proto/decode_test.go:123 零长度 bytes 解码为非 nil 空切片
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:349/408 TestBytes/TestField 长度前缀与字段边界
tags: [L10, L13, decoder, packed-repeated, zigzag, map-entry, duplicate-key, nil-vs-empty, merge, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机记录轮次"
---

# 场景

生成消息的容器解码器：`repeated int32` 同时接受 packed 与 unpacked、`repeated sint32` 走 zigzag、`repeated string/bytes` 逐元素追加（零长度 bytes 必须非 nil）、`map<string,int64>` 与 `map<int32,string>` 的 entry 解析（缺失 key/value 取默认、entry 内未知字段跳过、重复键 last-wins）。验证 merge 语义（repeated 追加、map 覆盖）、nil 与空容器区分、不别名输入 buffer，以及随机编码/解码往返。

## 期望行为

1. 全字段消息编码后解码，与源逐项相等；空字符串、空 bytes 元素、空 map key/value、`MinInt32/MaxInt32/MinInt64` 均保留；
2. 重编码结果确定（map 键排序、packed repeated），与首次编码逐字节一致；
3. 空输入解码得到全 nil 容器；非 nil 空容器编码为空字节；空 packed 字段不产生元素；单个零长度 bytes 元素解码为非 nil 空切片；
4. unpacked repeated 与 packed 等价；负 int32 的十字节 varint 与 zigzag 极值往返正确；
5. map 重复键后者覆盖；entry 缺少 value/key 时取 0/"" 默认值；空 entry 生成 `""→0`；entry 内未知字段被跳过；
6. merge 模式：repeated/字符串追加、map 同键覆盖并新增键、消息未出现的字段保留；非 merge 模式整体重置；
7. 错误路径：已知字段 wire type 错误、截断的 packed varint/字符串/entry、map key/value wire type 错误、字段号 0、wire type 6 均报错；
8. 解码后改写并释放输入 buffer，多轮 GC 后字符串/bytes/map 内容与重编码结果不变；
9. `TORTURE_ROUNDS × 60` 条 `UAF_SEED` 随机消息（含极值、空元素、随机 map 键）编码-解码往返相等且重编码幂等，周期性 GC 后稳定；
10. 最后一行 `ALL PASS l10_map_slice_decoder`。

## 失败判读

- 零长度 bytes 变 nil：用 `append([]byte(nil), p...)` 构造而非 `make/copy`；
- map 重复键保留旧值：entry 解码未按到达顺序覆盖；
- map 缺 value 时丢条目：entry 无字段时未写入默认键值；
- 截断 entry 被接受：entry 长度未按 wire 边界消费；
- merge 后重复元素丢失：decode 前无条件 reset；
- 重编码顺序漂移：map 键未排序或 packed 未归一；
- GC 后乱码：字符串/bytes/map 值与输入 buffer 别名；
- 随机失败可凭 seed= 复现；`TORTURE_ROUNDS` 下不稳定说明编码非幂等。

## 参数

- `UAF_SEED`：随机消息种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_map_slice_decoder/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
