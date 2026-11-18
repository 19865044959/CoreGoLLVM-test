---
id: l07_string_bytes_zero_copy
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - docs/DIMENSIONS.md: L07 unsafe 与零拷贝（string↔[]byte / 别名分析）
tags: [L07, unsafe, string, bytes, alias]
requires: []
runtime: "<3s"
---

# 场景

序列化/HTTP 库最常见的零拷贝手法：用 `unsafe.Pointer` 在 string 与 []byte 之间转换（`bytesToString` / `stringToBytes`），并与复制语义 `string(src)` 对照。GC 后别名视图必须仍指向有效数据。

# 期望行为

1. `bytesToString(src)` 得到的 string 在 GC 后内容正确；
2. `string(src)` 副本不受源切片修改影响，而零拷贝视图能观察到修改；
3. 堆上 string 转成 []byte 后经 GC 仍可读；
4. 结构体字段中的 string/[]byte 视图在 GC 后正确；
5. 最后一行 `ALL PASS l07_string_bytes_zero_copy`。

# 失败判读

- 内容错乱/崩溃：转换把指针/长度放错，或 GC 后底层数组被回收；
- 副本被源修改污染：复制语义被错误优化成别名；
- 仅 efence 下崩溃：底层数组已 unmap（UAF）。

# 参数

无随机路径；`unsafe` 行为与架构无关（长度/数据字段布局由 reflect.Header 描述）。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_string_bytes_zero_copy/*.go
GODEBUG=clobberfree=1 /tmp/case
```
