---
id: l13_strings_reader_statemachine
dim: L13_runtime_ds
priority: P0
sources:
  - echo@v4.13.4: bind_test.go:203 `strings.NewReader(userJSON)` 作为请求体绑定输入
  - caddy@v2.10.0: cmd/main_test.go:156 `parseEnvFile(strings.NewReader(tc.input))` 逐行读取配置
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:75 `struct{ io.Reader }{strings.NewReader(srcData)}` 包装读取
tags: [L13, strings.Reader, seek, readat, unread, readrune, writeto, reset, state-machine, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 HTTP 绑定/配置解析使用 `strings.Reader` 的完整读取状态机（echo 请求体、caddy env 文件与响应包装）：

1. 4096 字节确定性文本：Len/Size、顺序 Read、ReadAt（不改变 offset）；
2. Seek 到 `-128/SeekEnd` 读尾段，再读越过 EOF；
3. ReadByte/UnreadByte、连续两次 UnreadByte 失败后的状态；
4. 非法负偏移 Seek 失败且不改变当前位置；ReadAt 越过末尾返回短读 + io.EOF；
5. `WriteTo` 从当前位置写尽并耗尽 reader；Reset 后 Len/Size 更新；
6. `ReadRune/UnreadRune` 的 2/3 字节 UTF-8 边界与 EOF；
7. 空串 reader 的 Read/ReadAt/Seek/UnreadByte 边界；
8. `runtime.GC()` 后 Reset 并整段 ReadAll，内容不变。

# 期望行为

1. 所有读取内容与偏移推进精确符合 io.Reader/io.Seeker 契约；
2. ReadAt 不影响当前 offset；非法 Seek/UnreadByte 返回错误且状态可继续使用；
3. WriteTo 写尽未读部分并使 Len 归零；
4. 多字节 rune 的 size 与 UnreadRune 回退正确；
5. GC 后内容完整；最后一行输出 `ALL PASS l13_strings_reader_statemachine`。

# 失败判读

- `ReadAt changed offset`：Reader 状态机偏移维护错误；
- `Seek from end` / `negative Seek must fail`：Seek 边界计算错误；
- `UnreadByte` 相关：回退状态标记错误；
- `WriteTo content`：WriterTo 路径读写位置错误；
- `ReadRune` size 错误：UTF-8 解码边界错误。

# 参数

- `UAF_SEED`：文本内容的 salt 与失败输出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_strings_reader_statemachine/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
