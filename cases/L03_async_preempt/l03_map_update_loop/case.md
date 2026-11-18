---
id: l03_map_update_loop
dim: L03_async_preempt
priority: P2
sources:
  - caddy@v2.10.0: admin_test.go:51 /config/ JSON 树按路径新增/替换 map 键，失败路径必须确定
  - badger@v4.5.0: value_test.go:207 100 个 32KB 大值写入后删除 45 个，再重写值日志并校验剩余值
tags: [L03, async-preempt, map-update, delete-reinsert, map-growth, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

在 1024 个整数键的 map 上循环更新、周期性删除与重新插入，值是指向带代数信息的不可变堆节点；每 128 轮显式 GC 并分配垃圾，GC pump 并发执行。主循环内周期校验刚写入/删除的键，结束后按键号顺序与 Range 汇总双重校验最终状态。

# 期望行为

1. 每次更新后立即读回同代节点；
2. 删除后键立即不可见，后续轮次可重新插入；
3. 主循环内的抽查与 present/gen/value 记录一致；
4. 最终按键号顺序检查的键集合、节点代数与值正确；
5. 最终 Range 的条目数、值的和与顺序无关，且等于参考值；
6. 主循环显式 GC 次数符合 `(rounds-1)/128+1`，GC pump 至少完成一轮；
7. 退出码 0，最后一行 `ALL PASS l03_map_update_loop`。

# 失败判读

- `FAIL: map update writes visible` / `map deletes remove keys`：map 写入或删除语义错误；
- `FAIL: map loads after update/delete`：GC 后值指针失效或代数错乱；
- `FAIL: per-key final state` / `final map count/sum`：最终键值与原记录不一致；
- `SIGSEGV`：map 中保留的指针未被写屏障/扫描处理。

# 参数

- `UAF_SEED`：决定更新、删除、重插索引的步长/偏移，失败信息带出种子；
- `TORTURE_ROUNDS`：放大主循环轮数，默认 8192。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_map_update_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=131072 UAF_SEED=2 /tmp/case
```
