---
id: l10_oneof_codec
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:787 oneof 未设置时保持空
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:841 oneof 同时设置多个分支报错
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:849 oneof 分支为 null 与显式值混合时的语义
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:25/150 Get/Has 访问器与零值/typed-nil 语义
  - protobuf-go@v1.36.6: proto/decode_test.go:110 typed-nil oneof 包装解码为实际值
tags: [L10, L06, oneof, codec, json, branch-table, typed-nil, null, duplicate-key, base64, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

oneof 的 JSON 判别式 codec：`{"kind":"<分支>","value":<值>}` 信封，分支 codec 表（text/count/blob/flag/enum/nested，字段号 1..6）负责各分支值编解码；typed-nil 与 `value:null` 归一为未设置；严格拒绝未知分支、未知/重复信封键、类型错误与尾随数据；blob 走 base64，text 走 JSON 转义，nested 走对象编码。验证编码确定性、跨分支替换、输入释放后 GC 与随机往返。

## 期望行为

1. 分支表 6 项、字段号唯一（1=text…6=nested）；
2. 编码输出确定：text 空串、count 0、flag false、blob 空字节、enum 0、nested 空对象均保留分支；nil/typed-nil/nested 空指针编码为 `null`；blob 编码为 base64；字符串按 JSON 规则转义；
3. 所有分支往返语义一致，且重编码与首次编码逐字节相同；键顺序无关、空白容忍；text 可被 count 分支替换；
4. `value:null`（任意分支）与顶层 `null` 均解码为未设置；nested 的 `{}` 解码为 `S=""` 的分支；
5. 错误路径：空输入、非法 JSON、非对象、缺失 kind/value、kind 为 null/非字符串、未知 kind、未知信封键、重复 kind/value、text 非字符串、count 浮点/字符串/溢出、flag 非布尔、blob 非法 base64、enum 字符串/浮点、nested 非对象、尾随数据均报错；
6. 解码结果（text 字符串、blob 字节）独立于输入 JSON 字节；改写并释放输入、多轮 GC 后仍可编码/解码；
7. 200 条 `UAF_SEED` 随机分支值（含 nil、typed-nil、空值、特殊字符、随机 blob）编码-解码语义一致且重编码规范，周期性 GC 后稳定；
8. 最后一行 `ALL PASS l10_oneof_codec`。

## 失败判读

- typed-nil 编码出 kind 信封：分支提取未检查包装指针是否为 nil；
- `value:null` 覆盖已有值：null 未按"未设置"处理；
- 重复键静默 last-wins：Token 扫描未检测重复 key；
- 未知键被接受：信封扫描未白名单校验；
- count 接受 `1.5`/`"5"`：使用了浮点解析或 JSON 数字宽松解析；
- blob 空串变 nil：base64 解码后未归一非 nil；
- 重编码不一致：kind/value 顺序或转义不稳定；
- GC 后乱码：字符串/blob 与输入 JSON 别名；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机分支值种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_oneof_codec/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
