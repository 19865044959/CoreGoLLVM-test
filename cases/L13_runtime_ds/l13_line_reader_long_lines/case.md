---
id: l13_line_reader_long_lines
dim: L13_runtime_ds
priority: P0
sources:
  - go-redis@v9.8.0: internal/proto/reader_test.go:71 ReadLine 读 8KB 行且只在末尾遇 CRLF，循环 ReadSlice 拼接
tags: [L13, bufio, readline, long-line, buffer-boundary, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 go-redis RESP 协议的 ReadLine：用 `bufio.Reader`（1KB 窗口）循环 `ReadSlice('\n')`，把 `ErrBufferFull` 的片段拼接成完整长行。数据源为 8KB 长行 + 短行 + 空行 + 100B 行 + 无换行尾行，底层按 `UAF_SEED` 随机分片投递。另覆盖行长度 1022..2048 贴住缓冲边界、CRLF 恰好跨缓冲的情形。GC 插在长行读出后和后续读取后，验证已拼好的行长字符串仍然有效。

# 期望行为

1. 8KB 长行返回长度 8194（含 CRLF），去行尾后与原始 8192B 内容逐字节一致；
2. 长行内容转为 string 后跨 GC 不变；
3. 后续 `short-1`、空行、100B 行、无换行尾行依次读取正确，无换行尾行返回数据 + `io.EOF`；
4. 行长度为 1022/1023/1024/1025/2047/2048 时，CRLF 跨缓冲仍能正确分行，每行内容正确，之后立即 EOF；
5. 仅 LF 的行可正确去尾；空输入返回 `io.EOF`；
6. 后续读取 + GC 后，先前保存的长行字符串仍然有效；
7. 最后一行 `ALL PASS l13_line_reader_long_lines`。

# 失败判读

- 长行被截断或长度不对：`ErrBufferFull` 后未继续拼接；
- CRLF 跨缓冲时多/少一个字节：行尾剥离逻辑在片段边界上出错；
- GC 后长行内容变化/崩溃：拼接结果指向了可复用的 bufio 缓冲；
- 尾行缺换行时误报 EOF 且丢数据：`ReadSlice` 返回 `(data, io.EOF)` 的处理顺序错误。

# 参数

- `UAF_SEED`：底层随机分片与行内容的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_line_reader_long_lines/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
