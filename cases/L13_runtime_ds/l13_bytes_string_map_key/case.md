---
id: l13_bytes_string_map_key
dim: L13_runtime_ds
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 TestStringReferring 解码 string 是否引用输入 []byte
  - sonic@v1.13.2: decoder/decoder_native_test.go:276 TestCopyString CopyString 开关下改写输入 buffer 的输出差异
  - sonic@v1.13.2: issue_test/issue100_test.go:30 TestLargeMapValue >128B 结构体作为 map value 的指针存储路径
tags: [L13, bytes-to-string, map-key, ownership, binary-key, growth, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`[]byte`→`string` 作为 map key 的所有权与二进制边界：用 128B 复用缓冲区生成 2000 个唯一 key（前 4 字节小端序号，其余含 0x00/0xFF/0x80 随机字节），`string(seg)` 转换后立即把源缓冲改写为 0xEE，再验证：

1. 原 key 仍可查且 value 的 key/blob 与内容一致；
2. 用全新缓冲重建同内容 key 查询成功，改写该缓冲不影响已有 key；
3. 从不同缓冲重复插入同内容 key：不新增条目、value 被替换；
4. 用另建的 `[]byte` 删除每 7 个 key，再从新缓冲重建部分 key（步长 14）；
5. 单字节 key 0x00..0xFF 共 256 个 + 空字符串 key；
6. 4KB 大 key 写入后异或改写源缓冲，GC 后仍可查；
7. 最终按排序 key 与模型 `map[string]int` 逐项比对，GC 后再比对一次。

GC 插在批量插入中（每 257 个）、删除后、重插后、大 key 后与最终校验前，均伴随垃圾分配。

# 期望行为

1. 2000 个 key 插入后 map 与模型大小均为 2000，无重复；
2. 源缓冲改写后原 key 仍可查，value 的 key/blob 等于原 key 内容；
3. 重建 key 查询全部命中，重建缓冲改写不影响 map；
4. 重复插入 20 个 key 后大小仍为 2000，对应 value id 更新为新值；
5. 删除 286 个、重插 143 个后大小正确，GC 后一致；
6. 256 个单字节 key 与空 key 全部可按新建 []byte 查询；
7. 4KB key 跨源缓冲改写与 GC 后仍命中、blob 完整；
8. 最终排序 key 集合、每个 value 的 id/key/blob 与模型一致，GC 后仍一致；
9. 最后一行 `ALL PASS l13_bytes_string_map_key`。

# 失败判读

- 源缓冲改写后 key 查不到/内容变化：`string([]byte)` 未拷贝或拷贝长度错误；
- 重复插入后 map 变大：二进制 key 的相等/哈希路径错误；
- 单字节/空 key 失败：短 key 的边界处理错误；
- GC 后 value/blob 乱码：map value 指针未被扫描（UAF）；
- 排序 key 与模型不一致：删除/重插时使用了错误的 key 构造。

# 参数

- `UAF_SEED`：二进制 key 随机字节与 4KB key 内容的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_bytes_string_map_key/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
