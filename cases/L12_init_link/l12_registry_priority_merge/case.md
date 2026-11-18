---
id: l12_registry_priority_merge
dim: L12_init_link
priority: P1
sources:
  - ent@v0.14.4: entc/gen/globalid_test.go:72 TestResolveConflicts 冲突注册表合并语义
  - gitea@v1.23.5: modules/setting/config_provider_test.go:13 TestConfigProviderBehaviors，:17 BuggyKeyOverwritten 覆盖/优先级行为
  - golangci-lint@v2.0.2: pkg/lint/lintersdb/validator_test.go:84 TestValidator_Validate_error 重复/非法配置校验
tags: [L12, module, registry, cross-package-init, blank-import, priority-merge, deterministic-order, freeze, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

`plugin_high` 与 `plugin_low` 两个兄弟包由 main 以空白导入触发 init，各自向共享 `reg` 注册同名 key。注册表不采用“先到先得”，而是确定性合并：优先级高者胜，同优先级 owner 字典序小者胜。因此胜者与未定义的跨包 init 顺序无关。main 的包级变量初始化阶段调用 `reg.Freeze()` 冻结注册表（此时两个插件 init 已完成），随后验证胜者、历史视图与冻结后的拒绝路径。

## 期望行为

1. `frozenAtImport==true`：main 的包级变量初始化晚于两个空白导入包的 init；
2. 总贡献数 6，key 排序为 `codec,high-only,low-only,tie`；
3. `codec` 胜者为 alpha（priority 10，data `high-codec`）；`tie` 同优先级 7 由 owner `aaa` 胜出（data `low-tie`）；`high-only`/`low-only` 各自唯一；
4. History 按 owner 排序：codec 为 alpha,beta；tie 为 aaa,zzz，与 init 顺序无关；
5. 冻结后注册更高优先级条目返回 `ErrFrozen`，总贡献数、key 集合与胜者均不改变；
6. 8 goroutine 并发调用 Winner/History/Total 并穿插显式 GC 时结果一致，同一 key 的 `Meta` 指针身份稳定；
7. GC 后胜者内容、History 长度与冻结状态不变；
8. 最后一行 `ALL PASS l12_registry_priority_merge`。

## 失败判读

- 胜者随 init 顺序变化：合并策略错误地依赖写入顺序；
- 同优先级未按 owner 决胜：tie-break 实现错误；
- 冻结后仍可写入：Freeze 检查缺失或 main 变量初始化早于插件 init；
- History 顺序不稳定：视图未排序，暴露 map/init 顺序；
- GC 后 `Meta` 指针或 Data 损坏：全局注册表指针扫描错误；
- 失败行 `seed=` 可用于复现并发读取起点。

## 参数

- `UAF_SEED`：决定并发 worker 的 key 轮换起点；断言与起点无关。

## 运行

```bash
cd cases/L12_init_link/l12_registry_priority_merge
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
