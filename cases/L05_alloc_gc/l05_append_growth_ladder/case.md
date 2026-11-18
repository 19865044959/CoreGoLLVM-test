---
id: l05_append_growth_ladder
dim: L05_alloc_gc
priority: P0
sources:
  - pgx@v5.7.4: internal/pgio/write_test.go:23 的 TestAppendUint16BufWithCapacityDoesNotAllocate：cap 足够时 append 不扩容
  - badger@v4.5.0: y/y_test.go:70 的 TestBufferWrite：初始 32B 容量逐轮翻倍写入并与 bytes.Buffer 对照
tags: [L05, append-growth, slice-growth, old-backing, snapshot, gc]
requires: []
runtime: "<3s"
---

# 场景

从 nil 开始逐项 append 驱动扩容，覆盖 []byte、[]int64、[]string、[]*struct
四类元素：记录容量阶梯，在若干长度处保留旧底层数组上的满容量快照，扩容与
显式 GC 后验证快照不变、新老元素完整；另用预分配 cap 的切片做不扩容对照。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_append_growth_ladder`；
2. 四类切片最终 `len==2048` 且 `cap>=len`，从 nil 增长至少发生 5 次容量变化；
3. 扩容前捕获的快照在后续 append/GC 后内容完全不变（旧底层数组未被误写）；
4. 指针元素与字符串元素在 GC 后仍指向正确内容；
5. 追加第二批元素后前 2048 项仍完整；
6. 预分配 cap=2048 的 []struct 在 append 到 2048 期间 cap 始终保持 2048；
7. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `capacity transitions` 不足：append 扩容路径异常或退化为每次重新分配；
- `snapshot ... changed`：扩容时旧底层数组被覆写，或切片别名/容量语义错误；
- `element ... corrupted`：扩容后指针/字符串元素丢失或错位；
- `prealloc slice grew`：make(cap) 路径被破坏。

## 参数

- `UAF_SEED`：决定各元素取值与字符串后缀；失败输出带种子；
- 无其他 TORTURE 参数；规模固定（2048 + 512）。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_append_growth_ladder/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
