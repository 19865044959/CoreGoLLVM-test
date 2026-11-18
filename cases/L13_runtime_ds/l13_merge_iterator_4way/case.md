---
id: l13_merge_iterator_4way
dim: L13_runtime_ds
priority: P0
sources:
  - badger@v4.5.0: table/merge_iterator_test.go:162 4 路有序迭代器正/反向归并，重复 key 取舍确定
tags: [L13, merge-iterator, heap, sorted, duplicate-key, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 badger 表合并迭代器：4 路内部有序的 `(key, *item)` 迭代器，分别取 2/3/5/7 的倍数作为 key（大量重复）。用 `container/heap` 做 4 路归并，正向输出升序、反向输出降序；同一 key 出现在多个源时规则固定为“源序号大者胜出”，输出只保留一条。GC 插在数据构建后、正/反向归并后，验证 item 指针在堆归并过程中仍有效。

# 期望行为

1. 4 个源各自严格升序且非空；
2. 正向归并结果严格升序，长度等于去重后的 key 集合大小；
3. 反向归并结果严格降序，且与正向结果互为逆序（key 与来源 src 都对得上）；
4. 每个重复 key 的来源等于该 key 的最大可选源号（0..3）；
5. 每个结果的 `val.text == "s<src>-k<key>"`、`val.n == key*10+src`；
6. 正向 key 之和等于去重集合的 key 之和；总条目数大于去重后条目数（确实发生了重复合并）；
7. 归并结果跨 GC 后指针与内容仍正确；
8. 最后一行 `ALL PASS l13_merge_iterator_4way`。

# 失败判读

- 顺序错乱：堆比较函数在 key 相等时的 tie-break 写反；
- 重复 key 出现两次或来源不对：重复规则与堆弹出顺序不一致；
- `val` 为 nil/文本错乱：归并过程持有的 `*item` 未被正确扫描（写屏障/栈位图问题）；
- 反向结果与正向不互为逆序：反向迭代器位置或比较方向错误。

# 参数

无随机路径，不使用 `UAF_SEED`。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_merge_iterator_4way/*.go
GODEBUG=clobberfree=1 /tmp/case
```
