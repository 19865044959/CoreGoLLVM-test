---
id: l18_partial_message_recovery
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - pgx@v5.7.4: pgproto3/frontend_test.go:35 TestFrontendReceiveInterrupted（半条消息返回错误，补齐后重读成功）
tags: [L18, framing, partial-read, buffer-lifecycle, GC]
requires: []
runtime: "<2s"
---

# 场景

把 pgproto3 `Frontend.Receive` 的分帧行为重写为纯内存协议：每条消息为 `type(1B) + length(4B, 含长度字自身) + body`。数据不足时 `Receive` 返回 `errNeedMore` 且**不消费任何字节**；补齐后可成功读出。用例覆盖：

- 对 3 条消息的每一个切分点：半 header/半 body → 错误 → 重复读 → GC → 补齐 → 成功；
- 半条消息期间 frontend 缓冲存活，GC 不丢字节；
- 返回的 `body` 是独立拷贝，后续读取/GC 不覆盖已保留结果；
- 两条消息粘包时按边界消费，不串包、不丢尾字节；
- 空 body、`length<4`、极大 length 且数据不足等边界。

# 期望行为

1. 任一不完整切分点首次读取返回 `errNeedMore`、`msg==nil`；无新数据重复读取结果相同且 `buffered()` 不变；
2. 补齐后 `Receive` 成功，类型与 body 逐字节等于发送值，缓冲恰好清空；
3. 保留的旧 `message.body` 在读取下一条消息并 GC 后仍完好；
4. 粘包场景先读 A 后读 B，A/B 内容不混、尾字节不丢；
5. `length<4` 返回 `errBadLen`，极大 length 返回 `errNeedMore`，均不 panic/OOM；
6. 退出码 0，最后一行 `ALL PASS l18_partial_message_recovery`。

# 失败判读

- `repeat read consumed data` / `buffered bytes changed`：错误路径消费了半条消息，补齐后必然丢数据；
- `completed payload mismatch` / `mixed`：帧长度或缓冲偏移错误，数据串包；
- `retained A body corrupted`：`body` 别名了会被复用的内部缓冲，属于生命周期/UAF；
- `not rejected` / `not need-more`：边界检查缺失；
- SIGSEGV/panic：长度计算溢出或切片越界。

# 参数

无随机路径；`UAF_SEED` 不影响结果。所有切分点 + 8 轮流水线均为固定序列。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_partial_message_recovery/*.go
GODEBUG=clobberfree=1 /tmp/case
```
