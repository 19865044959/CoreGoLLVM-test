---
id: l13_slice_growth_alias
dim: L13_runtime_ds
priority: P0
sources:
  - badger@v4.5.0: y/y_test.go:70 TestBufferWrite 初始 32B 容量逐轮翻倍写入
  - sonic@v1.13.2: ast/buffer_test.go:42 Test_linkedPairs_Push 分块切片超过块容量后继续追加
  - go-redis@v9.8.0: internal/proto/writer_test.go:93 BenchmarkWriteBuffer_Append 复用 buffer 反复 Append 的扩容路径
tags: [L13, slice, append-growth, capacity, aliasing, pointer, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

覆盖 append 扩容与共享底层数组语义：

1. `[]*gnode` 从 nil 逐个 append 到 700 项，每步断言 `cap>=len`、cap 单调不降，每 64 次 GC 后按原指针身份逐项校验；
2. `base := make([]*gnode,4,12)`，`sub := base[:2]` 后 append：新元素写入父数组 index 2，但 `len(base)` 不变；GC 后父子视图都有效；
3. 满容量 `append` 必须分配新数组：修改新数组元素不得影响旧数组，GC 后两者内容各自正确；
4. `[]string` 手工翻倍增长 300 项，校验旧内容完整复制；
5. `UAF_SEED` 驱动 500 次随机 append 批次/截断/覆盖，参考 id 列表逐项校验，每 37 步显式 GC。

所有校验只比较指针身份和确定内容，不打印 cap 数值或地址。

# 期望行为

1. append 过程中 `cap>=len` 且 cap 不下降，700 项指针身份与内容跨 GC 不变；
2. 容量内 append 写父数组可见但父长度不变；
3. 满容量 append 产生独立新数组，旧数组不受新数组写入影响；
4. 手工翻倍后所有字符串顺序与内容正确；
5. 随机 append/截断后切片模型与参考完全一致；
6. 最后一行 `ALL PASS l13_slice_growth_alias`。

# 失败判读

- `append growth: cap<len` / `cap decreased`：slice 头增长路径错误；
- `alias:` / `full append:`：append 复制判定或共享底层数组语义错误；
- `pointers corrupted after GC`：扩容后旧/新数组指针未被正确保留；
- 超时：随机路径死循环。

# 参数

- `UAF_SEED`：随机 append/截断序列种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_slice_growth_alias ./cases/L13_runtime_ds/l13_slice_growth_alias/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_slice_growth_alias
```
