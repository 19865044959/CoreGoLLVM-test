---
id: l10_resolver_target_boundary
dim: L10_codegen
priority: P1
sources:
  - grpc-go@v1.71.1: clientconn_test.go:825 TestClientConnCanonicalTarget 目标规范化（默认 passthrough、未知 scheme、无斜杠形式）
  - grpc-go@v1.71.1: resolver/resolver_test.go:37 TestValidateEndpoints 端点合法性
  - grpc-go@v1.71.1: resolver/map_test.go:74 AddressMap 地址键语义
  - grpc-go@v1.71.1: resolver/map_test.go:176 EndpointMap 端点地址集合与顺序无关
  - grpc-go@v1.71.1: resolver/manual/manual_test.go:31 手动 resolver 更新路径
tags: [L10, L12, resolver, target-parsing, IPv6, percent-escape, weighted-endpoints, dedup, generation-guard, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

生成式 resolver 基础设施的边界组合：目标语法 `scheme://authority/endpoint`，支持 `dns:host:port` 无斜杠形式、IPv6 authority、endpoint 百分号解码与 canonical 重编码、未知 scheme/无 scheme 回落 passthrough；scheme builder 注册表（dns/passthrough/static/unix）负责拆分、去重与校验；端点展开按 priority 稳定排序、按 weight 重复地址、跨端点地址去重；resolver 状态用 generation 拒绝陈旧更新，非法更新保持状态原子。

## 期望行为

1. 12 组目标解析金标：scheme/authority/endpoint/canonical/fallback 正确，canonical 可再次解析且稳定；
2. 13 类非法目标（空、空格、非法 scheme、query/fragment、未闭合 IPv6、userinfo、坏百分号、控制字符等）被拒绝；未知 scheme 的 opaque 回落保留原始 `%` 序列；
3. 默认注册 4 个 scheme；重复/空 scheme/nil builder/冻结后注册被拒绝；冻结后仍可解析；
4. dns 对逗号列表大小写不敏感去重、空 endpoint 回退 authority；static 用 `net.SplitHostPort` 校验并去重；unix 路径规范化；passthrough 空 endpoint 报错；
5. 未知 scheme 回落 passthrough 并标记 fallback，地址为原始目标；
6. 端点展开：3:1 权重产生 `A,A,A,B`；多地址权重按轮重复；跨端点重复地址首次出现者胜；priority 升序稳定；空列表合法；
7. 0/负权重、空地址、端点内重复地址、负 priority 全部报错；
8. generation 守卫：相同/更旧 generation 报 stale 且不改状态；新 generation 替换；非法更新不改变 generation/地址；空更新清空；
9. 状态、解析结果与目标在 GC 后不变；
10. `UAF_SEED` 300 轮随机目标 canonical 往返 + 随机端点权重的独立模型对账。

## 失败判读

- `FAIL: ...`：目标拆分、canonical 转义、builder 去重或权重排序错误；
- `canonical reparse` 失败：canonical 形式不可逆（常见于 `%2F`/逗号/IPv6 转义遗漏）；
- `stale update` / `failed update leaves ...` 失败：generation 比较或原子提交错误；
- `torture schedule` 失败：展开顺序/重复次数与独立模型不符，带 `UAF_SEED`；
- `SIGSEGV` / `found bad pointer`：状态快照或地址切片在 GC 后失效。

## 参数

- `UAF_SEED`：随机目标与端点配置种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_resolver_target_boundary/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
