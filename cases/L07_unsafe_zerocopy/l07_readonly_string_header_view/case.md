---
id: l07_readonly_string_header_view
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 直接当作 *string 读取
  - sonic@v1.13.2: internal/caching/hashing_test.go:50 TestHashing_Fnv1a 读取 string 头/字节计算 FNV
  - fiber@v2.52.6: utils/convert_test.go:44 Benchmark_UnsafeBytes string→[]byte 只读视图
tags: [L07, unsafe, string-header, read-only, KeepAlive, FNV]
requires: []
runtime: "<3s"
---

# 场景

把 string 视为 `{data unsafe.Pointer; len int}` 的只读 header：读取字面量、子串、堆 string、结构体 string 字段、含 NUL/UTF-8 的字节序列的 data/len，派生只读字节视图与零拷贝 string，并只保留 header 跨显式 GC。任何路径都不写入 string 数据，验证视图不会修改源内容。

# 期望行为

1. 字面量 header 的 len 为字节长度，data 等于 `unsafe.StringData`；
2. 每个子串 header 的 data 等于父串 data + i，len 递减，只读字节与子串内容一致；
3. 空串 header 的 len=0，派生字节视图长度为 0；
4. 堆 string 的 header 跨 GC 后 data/len 有效，FNV-1a 校验和不变；原 string 变量置空不影响；
5. 从 header 重建的 `unsafe.String` 与原始内容相等，GC 后内容与 data 指针都不变；
6. 结构体 string 字段的 header 在结构体本身不可达并 GC 后仍可读；
7. 含 NUL 与 UTF-8 字节的 header 长度按字节计，只读视图逐字节一致，GC 后不变；
8. 64 个堆 string 的 header 批量保留，跨 GC 后长度与 FNV 校验和全部正确；
9. 反复读取后校验和不变，证明只读视图未修改数据；
10. 最后一行 `ALL PASS l07_readonly_string_header_view`。

# 失败判读

- header 长度/data 不符：string 头布局理解或偏移计算错误；
- GC 后内容错乱/efence 崩溃：header 中的 data 指针未被当作 GC 根，string 底层数组被回收；
- 子串偏移不符：切片头数据指针计算错误；
- 只读路径出现数据变化：用例误写内存（禁止写入 string 数据）；
- 仅 stress/k8s 失败：GOGC=1 下 header 变量未被栈位图标记。

# 参数

- `UAF_SEED`：决定堆 string 长度（24..47）与批量内容偏移，失败信息带种子；
- 不写入 string 数据；64 位目标。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_readonly_string_header_view/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
