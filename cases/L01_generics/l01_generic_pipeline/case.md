---
id: l01_generic_pipeline
dim: L01_generics
priority: P2
sources:
  - pgx@v5.7.4: pipeline_test.go:12 无 prepared/describe 的 pipeline 连发两条查询并 Sync，按序读结果
  - go-redis@v9.8.0: pipeline_test.go:74 逐级增大到 15 条命令的 pipeline，结果数与类型一致
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/pipe_test.go:30 NewPipe[int] 批量写入、读端按序累积、Close 后 EOF
tags: [L01, generics, pipeline, batching, channel, ordering, fail-fast, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认；TORTURE_ROUNDS 放大命令/元素数，上限 32）"
---

# 场景

两层泛型 pipeline：(a) `Pipeline[T]` 模拟 pgx/go-redis 批量命令——先 Submit 闭包命令、显式 GC（命令闭包持有全部 `*rec`）、再 Flush 顺序执行；含失败短路管道，第 5 条返回 sentinel error，验证已完成结果长度为 5、错误可 `errors.Is`、后续命令未执行。(b) 三级通道 `stage[T]`（单 goroutine 保序、输入关闭后关闭输出）串成 `-a/-b/-c` 变换流水线，喂入 n 个指针后逐条校验顺序、指针身份与变换结果，排空过程中每 64 条 GC 一次。最后以 `Pipeline[int64]` 二次实例化。TORTURE_ROUNDS 放大规模。

# 期望行为

1. 批量 Flush 结果数==n，results[i] 与 items[i] 为同一指针，id/label 变换正确；
2. 失败管道在错误处短路，done 长度 5，错误 wrap 后 `errors.Is(errBoom)` 成立，第 5 条及之后的命令未修改目标；
3. 通道流水线输出严格保序（第 i 条对应 items[i]），标签/ID 逐级累加正确，GC 穿插不改变结果；
4. 输出条数==n，所有 stage goroutine 正常退出（无死锁、无额外输出）；
5. `Pipeline[int64]` 的 32 个结果在 GC 后数值正确。

# 失败判读

- `order/pointer mismatch`：通道 stage 并发度>1 或关闭顺序错误导致乱序；
- 死锁/超时：输入 channel 无缓冲且先写后读，或输出未排空；
- `post-error command was executed`：Flush 未在错误处 return，短路语义错误。

# 参数

- `TORTURE_ROUNDS`：默认 1，最大 32；n=256*rounds；
- `UAF_SEED`：决定数据内容与部分 ID 位移，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_pipeline/*.go
UAF_SEED=7 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
