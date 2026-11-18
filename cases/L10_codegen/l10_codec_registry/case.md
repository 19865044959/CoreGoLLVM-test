---
id: l10_codec_registry
dim: L10_codegen
priority: P2
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 同名 codec/compressor 重复注册后最新注册生效，名称列表去重
  - grpc-go@v1.71.1: encoding/encoding_test.go:114 编解码错误沿调用链返回，不 panic
  - kitex@v0.13.0: pkg/remote/codec/thrift/codec_apache_test.go:56 全局读写 codec 函数注册/覆盖
  - protobuf-go@v1.36.6: proto/methods_test.go:45 生成消息 Marshal/Unmarshal 快速路径与通用路径一致
tags: [L10, L06, codec-registry, alias, negotiation, q-value, snapshot, freeze, gzip, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机注册/协商轮次"
---

# 场景

RPC 运行时 codec 注册表：名称按大小写/参数/内置 MIME 归一（`application/json; charset=utf-8`→json），`Register` 最新注册覆盖、空名/nil 拒绝，别名表拒绝重复添加，`Names` 去重排序；内容协商按 content-type 优先、Accept 列表 q 值降序且同 q 保序、全部不可接受时报错、空列表回落到默认 codec；`Snapshot` 与后续注册隔离，注册表可冻结；codec 与 gzip compressor 组成编码管线并验证错误传播。

## 期望行为

1. 内置 MIME 别名与自定义别名均能解析到已注册 codec，大小写与 `;` 参数不敏感；未知名称返回 false；
2. 同名重复注册以最新为准且不增加名称数；空名称返回 `ErrBadName`，nil codec 返回 `ErrNilCodec`；重复别名返回 `ErrDupAlias`，别名目标必须已注册；
3. 默认 codec 可设置/切换；未知默认设置报错；
4. 协商：content-type 非空且已注册时优先于 Accept；未知 content-type 报错；Accept 中未知项被跳过，q 大者优先，q 相同按出现顺序，q=0 视为不可接受；全不支持时返回 `ErrNoAccept`；两者都空时用默认；
5. 快照包含当时的名称、别名与默认值；快照创建后注册新 codec 不影响快照，且快照仍能解析别名；
6. 冻结后注册返回 `ErrFrozen`，查询/快照仍可用；compressor 注册表同样支持别名（x-gzip）、冻结与名称排序；
7. 管线：json/text/binary codec 与 identity/gzip 组合的编码-解码往返正确；未知 codec/encoding 报错；损坏的 gzip 数据报错；
8. 保留快照与压缩结果、丢弃临时对象后多轮 GC，仍可查询/解码；
9. `TORTURE_ROUNDS × 80` 次 `UAF_SEED` 随机注册/查询/协商/编解码与模型一致，每 40 次快照校验名称排序、数量与条目，周期性 GC 后稳定；
10. 最后一行 `ALL PASS l10_codec_registry`。

## 失败判读

- MIME 参数未剥离：查找键包含 `; charset=...`；
- 重复注册产生两个名字：注册前未按归一名称合并；
- q 值顺序错：未按 q 降序或同 q 未保序；
- 快照随新注册变化：快照持有原 map 而非副本；
- 冻结后仍能注册：冻结标志检查顺序错误；
- 管线先压缩后编码：encode/decode 顺序颠倒；
- 损坏压缩数据未报错：忽略了 gzip Reader 的 Read/Close 错误；
- GC 后崩溃：快照/结果未持有 codec 或字节；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机注册/协商种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_codec_registry/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
