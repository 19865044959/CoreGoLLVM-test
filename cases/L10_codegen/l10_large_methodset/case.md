---
id: l10_large_methodset
dim: L10_codegen
priority: P2
sources:
  - ent@v0.14.4: entc/integration/hooks/hooks_test.go:755 生成代码强类型遍历方法链
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 + internal/endtoend/testdata/alias/postgresql/pgx/v5/go/db.go:14 Querier 接口方法集
  - protobuf-go@v1.36.6: proto/reset_test.go:15 生成消息方法集
tags: [L10, L06, method-set, interface-dispatch, method-value, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大"
---

# 场景

手写生成风格的大方法集：160 个方法的 `bigAPI` 接口与 `*bigImpl` 实现，每个方法访问 receiver 的 slice/指针字段；`callAll` 通过接口一次性调用全部 160 个方法。验证接口方法数、方法值（method value）捕获 receiver 后跨 GC 调用、类型断言回到实现类型、GC 后 receiver 数据一致性。`TORTURE_ROUNDS` 放大轮次。

## 期望行为

1. `reflect` 观察到 `bigAPI` 方法数 = 160，`*bigImpl` 满足接口（编译期断言）；
2. 初始 `callAll` 结果等于逐方法公式求和；
3. 取 `Op042`/`Op159` 方法值 → GC → 调用结果仍正确；
4. 每轮修改 slice/指针载荷与 bias 后，`callAll` 及 6 个抽查方法与公式一致，类型断言回 `*bigImpl` 恒等；
5. `TORTURE_ROUNDS>1` 时重置为可手算输入，两次 `callAll` 结果相同且等于 `n*(n+1)` 形态的期望值；
6. 最后一行 `ALL PASS l10_large_methodset`。

## 失败判读

- 接口方法数不符/编译期断言失败：生成方法缺失或 receiver 类型不一致；
- GC 后方法值返回旧值或崩溃：method value 未保活 receiver，或指针字段未正确扫描；
- 单项抽查失败：方法体内固定索引与 `data` 长度不匹配（生成本用例的脚本 bug）；
- `TORTURE_ROUNDS` 下结果不稳定：数据重置不完整。

## 参数

- `UAF_SEED`：随机种子占位（方法集本身固定）；
- `TORTURE_ROUNDS`：轮次倍数（默认 1，上限 200），默认 64 轮 × 倍数。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_large_methodset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
