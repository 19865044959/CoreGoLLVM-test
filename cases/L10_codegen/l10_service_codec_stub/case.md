---
id: l10_service_codec_stub
dim: L10_codegen
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 TestDuplicateCompressorRegister 同名 codec 重复注册后注册者生效且名称去重
  - kitex@v0.13.0: pkg/remote/codec/thrift/codec_apache_test.go:56 TestApacheMarshalUnmarshal 全局 codec 读写函数覆盖
  - protobuf-go@v1.36.6: proto/methods_test.go:45 TestLegacyMarshalMethod 生成消息 Marshal/Unmarshal 快速路径
tags: [L10, L06, rpc-stub, codec, codec-negotiation, json, golden-bytes, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

手写生成风格 service stub：每个 method descriptor 自带默认 codec（echo=binary、upper=json），调用方可按请求覆盖 codec；全局 codec 注册表支持同名覆盖与名称去重。binary codec 使用固定 big-endian 长度前缀帧，json codec 使用 `encoding/json`。覆盖 golden 字节、协商往返、重复注册、错误路径、输入缓冲拷贝与 GC。

## 期望行为

1. 方法名排序 `[echo fail upper]`、codec 名排序 `[bad binary json]`，各方法默认 codec 正确；
2. binary 请求/响应与 JSON 请求的输出与硬编码 golden 逐字节一致；binary 解码把 payload 拷出输入缓冲，改写输入后消息不变；
3. echo 默认 binary、upper 默认 json；显式 codec 覆盖（upper+binary、echo+json）仍正确往返；
4. 同名 codec 注册覆盖后名称列表仍只有一项，stub 后续调用使用新实现；
5. 错误路径：未知方法、nil 请求、未知 codec、坏 codec marshal、handler status 13、transport 错误透传、maxRecv 超限、响应 seq 不匹配、客户端/服务端 codec 不一致、服务端无法解码请求；
6. GC 后 codec 名称表、保留响应与调用路径仍可用；
7. 260 次 `UAF_SEED` 随机调用（method/body/seq/tags/codec 随机）body/seq 与独立模型一致，周期性 GC 后复核；
8. 最后一行 `ALL PASS l10_service_codec_stub`。

## 失败判读

- golden 不符：帧字段顺序/端序/长度前缀错误；
- 往返 body 错：codec 协商结果与服务端不一致；
- 覆写后旧实现生效：注册表写入顺序错误；
- 错误类型不符：错误未 `%w` 包装导致 `errors.Is/As` 失效；
- 改写输入后消息变化：解码未拷贝 body；
- GC 后崩溃：codec/响应/注册表引用未保活；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机 method/body/seq/tags/codec 种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_service_codec_stub/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
