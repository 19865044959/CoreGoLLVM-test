---
id: l13_bytes_buffer_alias_grow
dim: L13_runtime_ds
priority: P1
sources:
  - badger@v4.5.0: y/y_test.go:104 `bytes.Equal(wb[:], b.Bytes())`，:143 `rb2 := b.Bytes()[:512]` 分段读取 Buffer.Bytes
  - echo@v4.13.4: bind_test.go:432 `body := bodyBuffer.Bytes()` 请求体快照
  - caddy@v2.10.0: caddytest/integration/listener_test.go:61 `buf.ReadFrom(request.Body)`，:66 `bytes.Equal(buf.Bytes(), body)`
tags: [L13, bytes.Buffer, bytes-alias, grow, truncate, next, unread, readfrom, writeto, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟请求体/日志缓冲复用 `bytes.Buffer` 的别名与增长语义（badger y.Buffer、echo 请求体、caddy 集成测试）：

1. 连续 WriteString 后校验 Len/String/Bytes；取 `Bytes()` 快照后继续写，快照必须稳定；
2. `Grow(n)` 保留内容，容量至少增加 n；
3. `Truncate(5)` 截断；`Truncate(len+1)` 必须 panic，且 panic 后状态仍可用；`Truncate(0)`；
4. `Next(4)`/ReadByte/UnreadByte 组合，写之后 UnreadByte 必须失败；
5. `ReadFrom` 追加、空 ReadFrom；`WriteTo` 只写未读部分并耗尽 Buffer；
6. `ReadRune/UnreadRune` 的 2/3 字节 UTF-8 边界；
7. Reset 后重新写入，旧内容不可见；
8. 阶段间显式 `runtime.GC()`，GC 后 Buffer 内容完整。

# 期望行为

1. Buffer 内容、长度与快照稳定性符合约定；
2. Grow/Truncate/Next/UnreadByte 的边界与 panic 行为正确；
3. ReadFrom 追加、WriteTo 写尽未读部分并使 Len 归零；
4. 多字节 rune 的读写与 UnreadRune 正确；
5. GC 后内容不变；最后一行输出 `ALL PASS l13_bytes_buffer_alias_grow`。

# 失败判读

- `snapshot changed by later write`：Bytes 返回的内部切片被错误共享/覆盖；
- `Grow capacity insufficient`：grow 未满足容量请求；
- `Truncate beyond len must panic`：边界检查缺失；
- `Next`/`UnreadByte` 相关：读写状态机错误；
- `WriteTo` 相关：未读区间计算错误；
- `ReadRune` size 错误：UTF-8 解码边界错误。

# 参数

- `UAF_SEED`：Grow 增量、追加片段 salt 与失败输出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_bytes_buffer_alias_grow/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
