---
id: l18_stream_cancel_reuse
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: stream_writer_test.go:373 TestStreamWriterCancel（Prepare/Write 后 Cancel，随后错误使用 API 不崩）
tags: [L18, state-machine, cancel, reuse, lifecycle]
requires: []
runtime: "<5s"
---

# 场景

模拟 badger `StreamWriter` 的 Prepare/Write/Flush/Cancel 状态机，并补齐“取消后再次使用”的契约：

- idle 状态 Write/Flush/Cancel 返回 `errNotPrepared`；
- Prepare 后重复 Prepare 返回 `errAlreadyPrepared`；
- Write 时拷贝输入，调用方随后改写输入不影响已接收数据；
- Cancel 丢弃 pending，取消后的 Write/Flush/Cancel 返回 `errCanceled`，但对象可再次 Prepare 复用；
- 30 轮 Flush/Cancel 交替，期间多次 GC；被取消批次不得进入 sink。

# 期望行为

1. 各状态迁移返回的错误与契约一致，任何错误使用都不 panic；
2. 正常批次 Flush 后 sink 内容与写入时字节逐字节一致（输入被改写也不受影响）；
3. Cancel 后的批次不出现在 sink；重新 Prepare 后的批次正常提交；
4. 30 轮复用并多次 GC 后，sink 条目数量、顺序、key/value 全部正确；
5. 最终 GC 后已提交数据仍完好；
6. 退出码 0，最后一行 `ALL PASS l18_stream_cancel_reuse`。

# 失败判读

- `unexpected panic`：取消路径仍访问已释放对象或切片越界；
- `wrong result`：状态机错误，或取消后状态未正确保持/复位；
- `write did not copy`：输入所有权处理错误（Write 未拷贝）；
- `canceled entry leaked into sink`：取消只清了指针但旧数据仍被提交；
- `sink[*] ... corrupted after final GC`：提交数据被 GC 提前回收。

# 参数

无随机路径；`UAF_SEED` 不影响结果。固定 30 轮复用，每 5 轮及取消路径穿插 GC。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_stream_cancel_reuse/*.go
GODEBUG=clobberfree=1 /tmp/case
```
