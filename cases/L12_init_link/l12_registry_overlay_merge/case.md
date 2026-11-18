---
id: l12_registry_overlay_merge
dim: L12_init_link
priority: P2
sources:
  - gitea@v1.23.5: modules/setting/config_provider_test.go:14 BuggyKeyOverwritten 覆盖/优先级行为
  - gitea@v1.23.5: modules/setting/storage_test.go:208 继承配置的局部 PATH 覆盖
  - ent@v0.14.4: entc/gen/globalid_test.go:72 TestResolveConflicts 冲突注册表合并
tags: [L12, module, registry, cross-package-init, blank-import, priority-merge, field-overlay, tombstone, freeze, deterministic-order, gc, TORTURE_ROUNDS]
requires: [pkgbuild]
runtime: "<10s（默认 8 worker × 300 迭代；TORTURE_ROUNDS 1..8）"
---

# 场景

`layerbase`/`layerteam`/`layerops` 三个包由 main 空白导入，在 init 中向共享 `reg` 注册同名 key。合并是字段级的：按 priority 从低到高应用非空字段；同优先级按 owner 字典序，较小 owner 最后应用而胜出；tombstone 清除其下所有已合并字段。main 的包级变量初始化中 `Freeze`，验证与未定义跨包 init 顺序无关的确定性结果。

## 期望行为

1. `frozenAtImport==true`：main 变量初始化晚于三个空白导入包的 init；
2. 贡献总数为 12；存活 key 排序为 `db,onlyteam,queue`，`cache`/`legacy` 被高优先级 tombstone 移除；
3. `db` 合并为 name `ops-db`、addr `team:1`、weight 5、sources `[base team ops]`、Meta tag `ops-db`/payload `m-ops`；
4. `onlyteam` 为 `only-team`/`team:9`/9、sources `[team]`、无 Meta；
5. `queue` 同优先级 6 由 owner `aaa` 在最后应用：name `aaa-name`，addr/weight 继承自 `bbb`（`bbb:6`/6），sources `[base bbb aaa]`；
6. History 是确定性应用序（db 为 base,team,ops；cache 为 base,team,tombstone-ops）；重复 Snapshot 的 Meta 指针身份稳定；
7. 冻结后 `Register` 返回 `ErrFrozen`，贡献数、快照与指纹不变；
8. 8 goroutine 并发 Snapshot/Lookup + 显式 GC 时指纹、Meta 指针与存活集合不变；
9. `TORTURE_ROUNDS` 放大并发轮次；
10. 最后一行 `ALL PASS l12_registry_overlay_merge`。

## 失败判读

- 胜者随 init 顺序变化：合并依赖注册顺序而非 priority/owner 排序；
- 字段覆盖错误：字段级 overlay 或同优先级 tie-break 实现错误；
- tombstone 未生效或错误清除更高优先级：tombstone 应用顺序错误；
- 冻结后仍可写：Freeze 检查缺失或 main 变量初始化早于插件 init；
- History/Snapshot 顺序不稳定：视图未排序，暴露 map/init 顺序；
- GC 后 Meta 指针或 payload 变化：全局注册表指针扫描错误。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 8）：并发迭代 = 300×rounds；
- `UAF_SEED`：决定并发 key 轮换起点与 GC 轮数；断言与起点无关，失败行带出种子。

## 运行

```bash
cd cases/L12_init_link/l12_registry_overlay_merge
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=7 /tmp/case
```
