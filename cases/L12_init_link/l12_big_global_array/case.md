---
id: l12_big_global_array
dim: L12_init_link
priority: P2
sources:
  - go-redis@v9.8.0: bench_decode_test.go:21 包级大字节表在测试二进制启动时初始化
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/mocks_test.go:706 包级 rawDesc 大切片 + init
  - fiber@v2.52.6: path_testcases_test.go:28 init 填充包级大测试表
tags: [L12, global-array, large-initializer, pointer-fields, write-barrier, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4096 项数组 + 1 轮变更；TORTURE_ROUNDS 放大变更/GC 轮数）"
---

# 场景

包级 `[4096]record` 大数组由包级变量初始化阶段的 `buildRecords()` 填充。每个 record 含 `string`、`[16]byte`、`int64` 与 `*payload`，payload 之间形成长度 4096 的链。主程序按 seed 选定子集执行 `TORTURE_ROUNDS` 轮 score/tag 变更，并把新 payload 指针追加到全局 slice，随后全量扫描数组与链表并在并发扫描期间穿插 GC。

# 期望行为

1. `buildRecords` 恰执行一次，数组长度 4096；
2. 每个元素 id/name/score/tag/payload 均符合构造公式，payload 链从 `records[0].payload` 起长度 4096；
3. 每轮变更使被选中的下标 score+1、tag[15]=轮次，未选中项不变；变更集合由 seed 决定且至少 1 项；
4. 全局 `mutated` 长度等于轮数，每个新 payload 的字节符合公式；
5. 8 goroutine 并发全量扫描（score 和、payload 抽样、链表计数）与主 goroutine GC 期间无错误；
6. GC 后全量校验仍通过、数组不重建、mutated 指针仍有效；
7. 默认 4096 项/1 轮；`TORTURE_ROUNDS` 放大变更与 GC 轮数（上限 16）；
8. 最后一行 `ALL PASS l12_big_global_array`。

# 失败判读

- 数组未在包变量初始化阶段构建：初始化表达式未执行；
- score/tag 与轮次不符：变更子集计算或写屏障后可见性错误；
- 链表断裂/成环：指针字段被 GC 错误回收或覆盖；
- 并发扫描失败：全局数组指针位图/扫描错误；
- GC 后 payload 内容变化：大对象/指针字段扫描错误；
- 失败行 `seed=` 可用于复现变更起点。

# 参数

- `TORTURE_ROUNDS`（默认 1，上限 16）：变更与显式 GC 的轮数；
- `UAF_SEED`：决定变更起点（`seed%137`）与抽样下标。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_big_global_array/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
