---
id: l10_codec_version_forward
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: proto/decode_test.go:148 unknown() 保存未识别字段的原始字节
  - protobuf-go@v1.36.6: proto/merge_test.go:512 Merge 时未知字段按到达顺序追加
  - protobuf-go@v1.36.6: proto/reset_test.go:26 Reset 后未知字段被清空
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 codec 重复注册与名称语义
  - kitex@v0.13.0: pkg/remote/codec/thrift/codec_apache_test.go:56 全局 codec 函数注册/覆盖
tags: [L10, L12, codec-registry, versioning, forward-compat, unknown-fields, raw-bytes, atomic-decode, group, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机 v2→v1→v2 轮次"
---

# 场景

版本化 codec 注册表：v1 认识字段 1–4，v2 增加 fixed64 字段 5 与 bytes 字段 6。v1 解码 v2 payload 时把字段 5/6 作为未知字段原样保存（含 group、fixed32/64、varint、bytes 与错误 wire type），v1 重编码把已知字段规范化后追加未知原始字节，v2 再解码即可恢复扩展字段。注册表按 `(name,version)` 拒绝重复，`Resolve(name,min,max)` 取范围内最高版本，支持冻结；解码失败对目标对象原子。

## 期望行为

1. v1 固定记录编码金标一致，往返字段相等；
2. v2 → v1 解码后扩展字段不可见但未知字段恰为 2 条（字段 5、6）；v1 重编码 → v2 解码后 score/note 恢复且未知字段清零；
3. 完整 v2→v1→v2 循环逐字节稳定；
4. 混合未知字段（varint/bytes/fixed64/fixed32/group）按到达顺序原样保存并逐字节重编码；输入 buffer 毒化 + GC 后仍一致；
5. 已知字段号配错 wire type 时按未知字段保留，不清空/覆盖已解码值；v2 同样保留；
6. 重复标量 last-wins、repeated 追加；
7. 8 类截断/非法 wire payload 返回错误且目标对象不变；
8. 注册表：重复版本、空名、版本 0、nil schema 被拒绝；解析 v1/v2/范围/未知名/倒置范围正确；冻结后拒绝注册但仍可解析；
9. `TORTURE_ROUNDS` 轮随机 v2 记录 + 注入未知字段经 v1 转码后，v2 逻辑字段与未知原始字节均与模型一致。

## 失败判读

- `FAIL: ...`：版本选择、未知字段保留或注册表语义错误；
- `unknown fields re-encode byte-identically` 失败：未知字段被丢弃、重排或重新编码；
- `failed decode leaves destination unchanged` 失败：解码先写目标再校验；
- `torture ... unknown mismatch`：v1 转码改变了注入的未知原始字节，失败信息带 `UAF_SEED`；
- 超时：随机轮次异常放大。

## 参数

- `UAF_SEED`：随机记录与未知字段注入种子；
- `TORTURE_ROUNDS`：随机轮次数，默认 250。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_codec_version_forward/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=3000 /tmp/case
```
