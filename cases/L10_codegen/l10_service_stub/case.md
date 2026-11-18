---
id: l10_service_stub
dim: L10_codegen
priority: P2
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 接口 codec 注册表、同名重复注册覆盖与名称去重
  - grpc-go@v1.71.1: rpc_util_test.go:351 接收消息大小限制与错误包装路径
  - kitex@v0.13.0: pkg/remote/codec/thrift/codec_apache_test.go:56 全局编解码函数注册/覆盖
tags: [L10, L06, rpc-stub, codec-registry, error-path, max-size, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大"
---

# 场景

手写生成风格 RPC stub：request/response 结构体、`codec` 接口与 binary codec 的 wire 编解码、codec 注册表（同名后注册覆盖、名称去重）、transport 抽象与本地 handler。stub 的 `Invoke` 走 marshal→RoundTrip→maxRecv 检查→unmarshal→seq 校验→status 错误转换。覆盖成功 Echo、unimplemented、服务端 status code、transport 错误、超限、畸形响应、seq 不匹配、nil 请求与坏 codec 路径。`TORTURE_ROUNDS` 放大调用轮次。

## 期望行为

1. 固定 Echo 往返：body 被服务端反转、seq 一致；请求/响应 GC 后内容不变；
2. 错误路径：未知方法 `ErrUnimplemented`；code 13 转换为 `statusError{13}`；transport 错误原样透传；客户端 maxRecv 触发 `ErrResourceExhausted`；坏响应/seq 不符返回 `ErrInvalidResponse`；nil 请求返回 `ErrInternal`；
3. 坏 codec：请求编码失败包装为 `ErrInternal`，响应解码失败包装为 `ErrInvalidResponse`；
4. 同名 codec 注册覆盖后 `get` 返回新实现、名称列表仍只有 1 项，新 stub 端到端使用覆盖实现；
5. 编码/解码都拷贝 body：改写编码后的缓冲或源 body 不影响已编/已解消息；
6. GC 后注册表、codec 名称与查询仍正确；
7. `UAF_SEED` 随机 body/seq 共 300×`TORTURE_ROUNDS` 次调用全部 Echo 正确，保留的请求/响应 GC 后仍一致；
8. 最后一行 `ALL PASS l10_service_stub`。

## 失败判读

- Echo 内容/seq 不符：wire 编解码或 stub 校验逻辑错误；
- 错误类型不匹配：错误未 `%w` 包装导致 `errors.Is/As` 失效；
- 超限未触发：maxRecv 检查位置错误或长度计算错；
- 覆盖注册后旧实现仍生效：注册表写入顺序错误；
- 改写缓冲后内容变化：codec 未拷贝 body；
- GC 后 stub/registry 崩溃：方法值或全局表引用丢失。

## 参数

- `UAF_SEED`：随机 body/seq 种子，失败输出带 seed；
- `TORTURE_ROUNDS`：调用轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_service_stub/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
