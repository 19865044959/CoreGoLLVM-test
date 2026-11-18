---
id: l10_enum_stringer
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:12 TestGoCamelCase 常量名规范化
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:45 TestGoSanitized 标识符合法化
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/enumprefix_test.go:13 TestStripEnumPrefix 枚举值前缀剥离
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 + internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:12-65 String/Valid/AllValues
tags: [L10, L11, enum, stringer, index-table, prefix-trim, text-marshal, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 `stringer` 生成的多份枚举产物：两个连续枚举（Color/Shape）各用大名称常量 + `[]uint16` 偏移表 + 前缀剥离后的切片，越界回退 `Color(n)`；一个稀疏值域枚举（Level）退化为 switch。另生成 `GoString`、`MarshalText`、`UnmarshalText` 与运行期反查 map。验证全量映射、偏移表结构、GC 后大字符串常量与子串存活，`UAF_SEED` 随机探测含越界值。

## 期望行为

1. Color/Shape 的偏移表长度为枚举数 + 1、严格递增、末项等于名称常量长度；名称常量无 NUL；
2. 全部合法值 `String()` 与独立期望表一致，`_name[_index[i]:_index[i+1]]` 切片结果一致；反查 map 大小正确；
3. 负值、上界、极大值回退为 `Color(n)`/`Shape(n)`；`%v` 使用 String，`%#v` 使用 GoString；
4. `MarshalText`/`UnmarshalText` 对合法值往返一致，非法值/畸形名称返回错误；
5. Level 的 -100/10/20/300 与 0/21/MinInt32/MaxInt32 映射正确；
6. GC 后名称常量哈希、保留的子串、枚举切片仍与 GC 前一致；
7. 3000 次 `UAF_SEED` 随机探测（三枚举、有效/越界混合）全部通过，周期性 GC；
8. 最后一行 `ALL PASS l10_enum_stringer`。

## 失败判读

- 某个值名称错位：偏移表与名称常量不一致（生成器拼接 bug）；
- 越界未回退：边界判断漏掉负数或上界；
- GC 后字符串变化/崩溃：名称常量或被保留子串未正确保活（编译器问题）；
- 反查/文本编解码失败：map 构建或大小写/空白处理错误；
- 退出码 1：任一 `ck` 失败。

## 参数

- `UAF_SEED`：随机探测序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_enum_stringer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
