---
id: l10_service_desc_table
dim: L10_codegen
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 TestDuplicateCompressorRegister 注册表重复注册与名称去重
  - protobuf-go@v1.36.6: internal/filedesc/desc_test.go:32 TestFile 字段/方法描述表构造
  - ent@v0.14.4: entc/gen/graph_test.go:68 TestNewGraph 节点/边描述与确定性排序
tags: [L10, L12, service-desc, method-table, duplicate-detect, atomic-register, closure, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 grpc/protobuf 生成代码的 service descriptor 表：8 个服务 × 16 个方法，每个方法含 handler 闭包、client/server streaming 标记，全局按 `"/pkg.Svc/Method"` 建 path 索引。注册器先校验（空描述符、空服务名、空/重复方法名、nil handler）再原子提交，重复服务返回 `ErrDuplicateService`，重复方法返回 `ErrDuplicateMethod`；注册后调用方修改原描述符不影响注册表。

## 期望行为

1. 8 个服务、128 条 path、服务名与 path 列表排序确定；每服务 16 方法，Metadata 保留；
2. server streaming 32 个、client streaming 16 个，标记与生成规则一致；
3. 全部 128 个 path 的 handler 分发结果正确；GC 后 handler 闭包保留的索引仍正确；
4. 重复服务名返回 `ErrDuplicateService`，注册表与 path 数不变；
5. 同一服务内重复方法名返回 `ErrDuplicateMethod`，整个服务不留下任何 path（原子性）；
6. nil/空服务名/空 HandlerType/空方法列表/空方法名/nil handler 均返回 `ErrInvalid`，注册表不变；
7. 不同服务可用同名方法；注册后修改原 `serviceDesc`/方法名/handler 不影响已注册表；
8. 未知 path 返回 `ErrNotFound`；
9. 600 次 `UAF_SEED` 随机服务/方法/请求分发全部正确，周期性 GC 后仍正确；
10. 最后一行 `ALL PASS l10_service_desc_table`。

## 失败判读

- path 数/排序不符：注册顺序或索引构建错误；
- handler 返回旧内容：闭包捕获了循环变量地址或注册表被调用方后续修改；
- 重复注册未被拒绝或已部分生效：校验与提交不是两阶段；
- 失败注册留下 path：提交前未完成全部校验；
- GC 后分发失败：闭包/描述符未被全局表保活；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机服务/方法/请求种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_service_desc_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
