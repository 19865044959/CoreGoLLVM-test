---
id: l07_json_token_truncation_alias
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 CopyString 开关决定解码 string 是否引用输入 []byte
  - goccy-json@v0.10.5: path_test.go:12 路径抽取返回输入子切片，视图不得复制
  - fasthttp@v1.59.0: args_test.go:150 PeekMulti 返回共享底层缓冲的子切片
tags: [L07, unsafe, json-token, truncation-boundary, byte-alias, escaped-copy, GC]
requires: []
runtime: "<10s"
---

# 场景

JSON token 别名的截断边界：手写扁平对象扫描器，未转义的 key/value（含数字）用
`unsafe.String` 直接引用输入字节，含转义的 token 解码到独立缓冲；空 string 不建立别名。
对完整文档的每一个真前缀都必须解析失败且不 panic，独立 token 的所有前缀同样拒绝。
模拟 sonic `CopyString` 开关、goccy/go-json 路径抽取子切片与 fasthttp `PeekMulti` 的共享
缓冲语义。

## 期望行为

1. 完整对象解析成功，token 数、key 顺序、类型与值全部匹配；
2. 对 `0..len(doc)-1` 的每个前缀长度，解析器都返回 false 且不 panic；
3. 独立 string token（普通/空/含转义）的每个真前缀都失败，只有完整 token 成功；
4. `12.`、`-`、`.5`、`1e5`、`tru`、`truex`、`nul` 等畸形 token 全部拒绝；
5. 未转义 key/value 的 `{data,len}` 指向输入对应偏移；转义值不与输入重叠；
6. 改写输入字节时未转义 token 立即变化，转义 token 不受影响；恢复后输入一致；
7. 丢弃输入并显式 GC 后，所有 token 的 key/value 仍完整可读；
8. 末行 `ALL PASS l07_json_token_truncation_alias`，失败退出 1。

## 失败判读

- 截断前缀被接受：边界检查缺失（越界读或提前返回成功）；
- panic：`rawString`/数字扫描在 `p.i >= len(b)` 时继续取字节；
- 转义值与输入别名：转义解码路径错误地直接引用输入；
- GC 后内容错乱：非转义 token 的 string 头未被当作根，或输入提前回收；
- 空 string 被判定为别名：`unsafe.StringData("")` 与输入偏移误比较。

## 参数

- `UAF_SEED`：决定文档中的数值与 padding 内容，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_json_token_truncation_alias/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
