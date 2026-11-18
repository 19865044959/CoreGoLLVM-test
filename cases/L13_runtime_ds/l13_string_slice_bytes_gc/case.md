---
id: l13_string_slice_bytes_gc
dim: L13_runtime_ds
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: 字符串/切片子串跨 GC
tags: [L13, string, substring, bytes, builder, gc]
requires: []
runtime: "<3s"
---

# 场景

服务/序列化库里的字符串常见操作：`[]byte→string`、子串、`strings.Builder`、用 `[]byte` 构造的字符串作为 map 键、Unicode 切片。GC 后这些字符串的底层数据必须仍然有效。

# 期望行为

1. 基础串、`base[7:27]`、`b[10:30]` 转换结果在 GC 后与预期一致；
2. Builder 生成 600 字节、前缀正确；
3. 100 个由 `[]byte` 构造的键（k00..k99）在 GC 后可查、值正确；
4. Unicode 子串 `u == "hello"`；
5. 最后一行 `ALL PASS l13_string_slice_bytes_gc`。

# 失败判读

- 内容变成乱码/垃圾：字符串底层数组被回收或复制逻辑错误；
- map 键查不到：字符串哈希/相等比较路径问题；
- 仅 efence 下崩溃：字符串底层数组已 unmap（UAF）。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_string_slice_bytes_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
```
