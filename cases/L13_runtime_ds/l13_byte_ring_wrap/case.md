---
id: l13_byte_ring_wrap
dim: L13_runtime_ds
priority: P0
sources:
  - etcd@v3.5.0: raft/tracker/inflights_test.go:22 TestInflightsAdd start/count/size 环绕填充
  - etcd@v3.5.0: raft/tracker/inflights_test.go:170 TestInflightFreeFirstOne 回绕后释放最旧元素
  - badger@v4.5.0: y/y_test.go:121 TestPagebufferReader 页缓冲读者跨页读取
tags: [L13, ring-buffer, wrap, overwrite, snapshot, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

固定容量字节环形缓冲（head/count）：

1. cap=16 固定边界：写 10B→读 5B→写 8B 造成尾部回绕；写满 16B；再写 3B 覆盖最旧；一次写 20B（超过容量只保留最后 16B）；consume(0)、consume(超量)、耗尽后复用；快照必须是独立拷贝；
2. cap=37 随机 4000 次操作：随机长度（含超过容量）写入、随机消费、随机快照，与 `[]byte` reference deque 逐步比对；每 13 步检查点、每 211 步 GC、每 500 步清空；
3. 收尾写满 → GC → 消费一半 → 再写回绕，验证 head/count 范围。

所有比较基于 `bytes.Equal`，不打印缓冲内容。

# 期望行为

1. 固定边界下写/读/覆盖/超容量写入语义与参考 deque 一致；
2. 随机 4000 步每一步快照与参考相同，GC 前后一致；
3. 清空后环形缓冲可正常复用；
4. 快照修改不影响环形缓冲；
5. head/count 始终满足 `0<=head<cap`、`0<=count<=cap`；
6. 最后一行 `ALL PASS l13_byte_ring_wrap`。

# 失败判读

- `ring fixed:` / `ring random:`：回绕写入位置、覆盖最旧或快照两段拼接错误；
- `snapshot after GC`：缓冲底层数组跨 GC 失效；
- `snapshot aliases ring`：快照未拷贝；
- `consume mismatch`：head/count 更新错误或消费越界；
- 超时：随机路径死循环。

# 参数

- `UAF_SEED`：随机读写序列种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_byte_ring_wrap ./cases/L13_runtime_ds/l13_byte_ring_wrap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_byte_ring_wrap
```
