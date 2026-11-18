---
id: l12_registry_duplicates
dim: L12_init_link
priority: P1
sources:
  - kitex@v0.13.0: pkg/stats/event_test.go:25 全局事件注册表重名 ErrDuplicated、冻结后 ErrNotAllowed
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 同名注册覆盖/去重与名字查找
tags: [L12, registry, duplicate-detection, stable-order, freeze, concurrency, gc]
requires: []
runtime: "<10s"
---

# 场景

启动期 init 向本地注册表登记 3 个内置实现，主程序验证重名检测不覆盖旧值、注册顺序无关的稳定排序输出、并发注册同一名字时恰好一个成功、冻结后拒绝新注册，并在并发只读 + GC 下校验内容。

# 期望行为

1. init 无错误，内置名字排序为 `[gob json text]`，Dump 为 `gob=1,json=2,text=3`；
2. 重复注册 `json` 返回 `errDuplicate`，表长度和价值不变；
3. `yaml` 注册成功，重复注册返回 `errDuplicate`；Dump 稳定含 `yaml=4`；
4. 24 goroutine 并发注册 `zstd`：恰好 1 个成功、23 个返回 `errDuplicate`，表长度只 +1；
5. 冻结后注册 `xml` 返回 `errFrozen`，Dump/名字列表不变；
6. 8 goroutine 并发只读 + 主 goroutine GC 期间所有查询正确；
7. GC 后条目指针、impl 数据与排序输出不变；
8. 最后一行 `ALL PASS l12_registry_duplicates`。

# 失败判读

- 并发注册成功数 ≠1：重名检测存在锁/原子性缺口；
- Dump 顺序变化：注册结果依赖 map 遍历顺序，缺少排序；
- 冻结后仍可注册：冻结标志未生效或存在旁路；
- GC 后 impl 数据损坏：全局注册表指针扫描/写屏障问题；
- 失败行 `seed=` 可用于复现并发读的 key 顺序。

# 参数

- `UAF_SEED`：决定并发只读阶段的起始 key 偏移；不影响断言结果，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_registry_duplicates/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
