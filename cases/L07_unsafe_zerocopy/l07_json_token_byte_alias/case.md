---
id: l07_json_token_byte_alias
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 CopyString 开关决定解码 string 是否引用输入 []byte
  - goccy-json@v0.10.5: path_test.go:12 路径抽取返回输入子切片，视图不得复制
  - fasthttp@v1.59.0: args_test.go:150 PeekMulti 返回共享底层缓冲的子切片
tags: [L07, unsafe, decoder, byte-alias, json-token, escaped-copy]
requires: []
runtime: "<10s"
---

# 场景

JSON token 解码器的字节别名：手写扁平对象扫描器，未转义的 string 与 number token 用
`unsafe.String(unsafe.SliceData(raw), len(raw))` 直接引用输入缓冲；含 `\" \\ \uXXXX` 等转义
的 key/value 解码到独立新缓冲。token 保存 key/value 视图、输入偏移与 copied 标志。
对标 sonic CopyString 开关与 goccy-json 路径抽取的零拷贝语义。

## 期望行为

1. 解析出的 token 数、key 顺序、kind（string/number/bool/null）与期望表一致；
2. 未转义 key/value 的 `unsafe.StringData` 指针等于输入缓冲对应偏移地址；
3. 转义 value/key 标记 copied，且其数据指针不指向输入缓冲；
4. 解析前后输入缓冲逐字节不变（解析器绝不原地改写）；
5. 改写输入缓冲字节时，未转义 token 立即观察到变化；恢复后一致；
6. 转义 token 不受输入改写影响（复制隔离）；
7. 丢弃输入缓冲、仅保留 token string 视图并 GC 后，13 个 token 的 key/value/kind 全部正确；
8. 末行 `ALL PASS l07_json_token_byte_alias`，失败退出 1。

## 失败判读

- token 值与输入不符：扫描偏移或转义解码错误；
- 指针别名断言失败：实际发生了复制，零拷贝路径未生效；
- 输入被改写：解析器原地归一化/写入了输入；
- GC 后 token 内容错乱/崩溃：view string 未被当作 GC 根；
- 转义值随输入变化：解码结果错误地引用了输入而非新缓冲。

## 参数

- `UAF_SEED`：决定两个数值/字符串 token 的内容，失败信息带种子；
- 无 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_json_token_byte_alias/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
