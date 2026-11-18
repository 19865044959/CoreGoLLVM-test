---
id: l10_resolver_table
dim: L10_codegen
priority: P1
sources:
  - grpc-go@v1.71.1: resolver/map_test.go:74 AddressMap 以 Address 核心字段为键，BalancerAttributes 不参与相等性；别名地址覆盖原值
  - grpc-go@v1.71.1: resolver/map_test.go:111/134/155 Delete 经别名删除原键，Keys/Values 返回确定集合
  - grpc-go@v1.71.1: resolver/map_test.go:176 EndpointMap 以端点地址集合为键，地址顺序不影响相等性
  - grpc-go@v1.71.1: resolver/resolver_test.go:37 ValidateEndpoints 目标与端点合法性校验
  - grpc-go@v1.71.1: resolver/manual/manual_test.go:31 手动 resolver 的注册/更新路径
tags: [L10, L12, resolver, scheme-registry, address-map, endpoint-map, alias-key, state-machine, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

手写生成器风格的 gRPC resolver 基础设施：`scheme://authority/endpoint` 目标解析、scheme→builder 注册表（大小写归一、重复注册报错、冻结）、AddressMap（`BalancerAttributes` 不参与键相等，别名地址覆盖原值）、EndpointMap（端点内地址集合与顺序无关）、resolver 状态机（IDLE→RESOLVING→RESOLVED/TRANSIENT_FAILURE）。验证注册表确定性、别名键语义、状态迁移，以及 GC 后解析结果不可变。

## 期望行为

1. `parseTarget` 对 `dns:///host:port`、`static://authority/a,b`、无 scheme 的 `plain:123` 解析正确；空 target、空 scheme、空 endpoint、`scheme://host`（无路径）均报错；scheme 统一小写；
2. builder 注册表 scheme 大小写不敏感，重复注册返回 `ErrDuplicated`，冻结后注册返回 `ErrFrozen` 且已有条目仍可查询，Names 排序确定；
3. Resolve 按 builder 返回顺序构造端点；未知 scheme、builder 返回空端点/空地址/重复端点/参数错误均返回错误；
4. AddressMap：仅 `Addr/ServerName/Attributes` 不同的 7 个地址合并为 6 个键，别名 Set 覆盖值、Delete 删除原键；Keys/Values 按核心键排序，删除不存在的地址不 panic；
5. EndpointMap：`[a1,a2]`、`[a2,a1]`、`[a1,a1,a2]` 视为同一键；不同地址集合不同键；Delete 经等价键生效；
6. 状态机：IDLE 初始；成功迁移到 RESOLVED 并保存端点；失败迁移到 TRANSIENT_FAILURE 且清空端点；再次成功后恢复 RESOLVED；
7. 保留端点、地址视图与注册表名字后丢弃临时构建器，多轮 GC 后内容不变；
8. 400 次 `UAF_SEED` 随机 Set/Delete 与独立模型逐项一致，周期性 GC 后 Len/Get/Keys 仍一致；
9. 最后一行 `ALL PASS l10_resolver_table`。

## 失败判读

- 别名地址导致 Len 增长：相等性把 `BalancerAttributes` 计入键；
- Get 返回旧值：别名 Set 未覆盖原条目；
- Keys/Values 顺序随运行变化：直接遍历 map 未排序；
- 端点集合顺序敏感：EndpointMap 键未对地址做排序/去重；
- 冻结后仍可注册：冻结标志未检查；
- 失败状态残留端点：失败路径未清空状态；
- GC 后崩溃/乱码：保留视图未持有底层数据；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机地址操作序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_resolver_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
