---
id: l07_byte_pool_alias_copy
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - fasthttp@v1.59.0: http_test.go:456 SwapBody 在池化对象间交换 []byte 所有权
  - klauspost-compress@v1.17.11: s2/writer_test.go:505 EncodeBuffer 后复用输入 buffer，输出不得保留引用
tags: [L07, unsafe, byte-pool, alias, copy-semantics]
requires: []
runtime: "<10s"
---

# 场景

字节池归还后缓冲会被下一位使用者覆写。库内部用 `unsafe.String` 生成零拷贝 string 时，调用方若在缓冲归还后继续使用该 string，就会读到新请求的数据；正确做法是在归还前 `string(buf)` 复制。用例用确定性的 free-list 池（保证复用同一底层数组）依次写入三条等长记录，对比零拷贝视图、复制版和归还前快照在 GC 与复用后的变化。

# 期望行为

1. 缓冲被持有时，零拷贝 string 与复制 string 都等于首条记录，GC 后不变；
2. 归还后 GC，池的空闲链表保活底层数组；再次 get 返回同一底层数组且 len=0；
3. 覆写缓冲后，零拷贝视图等于新记录，复制版仍等于首条记录；
4. 归还前复制得到的快照在第二次复用和 GC 后保持稳定；
5. 最终池化缓冲内容、长度、容量正确；
6. 最后一行 `ALL PASS l07_byte_pool_alias_copy`。

# 失败判读

- 复用后复制版被污染：`string(b)` 被错误优化为零拷贝；
- 零拷贝视图在覆写后仍是旧内容：string 头被意外复制，或缓冲未被真正复用；
- GC 后内容错乱：池/局部引用未保活底层数组，`clobberfree` 改写空闲内存；
- 池返回的数组指针与视图不一致：池实现或切片头处理错误。

# 参数

- `UAF_SEED`：选择三条记录的 id 与 state 组合，失败信息带种子；
- 记录固定 16 字节，保证别名 string 长度不随复用变化。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_byte_pool_alias_copy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
