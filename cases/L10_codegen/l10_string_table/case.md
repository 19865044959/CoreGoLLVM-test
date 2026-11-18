---
id: l10_string_table
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:12 GoCamelCase 名称规范化（生成标识符）
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:67 枚举/字段 JSON 名推导，供生成代码名称表去重
  - protobuf-go@v1.36.6: compiler/protogen/protogen_test.go:91 import path→包名映射与一致性
  - protobuf-go@v1.36.6: internal/filedesc/desc_test.go:32 描述符名称/JSON 名表构造
  - ent@v0.14.4: entc/gen/type_test.go:299 生成标识符合法化与去重
tags: [L10, L13, string-table, interning, hashing, growth, freeze, snapshot, json-name, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

生成代码的名称/符号字符串表：把消息名、字段名、JSON 名、枚举值、import path 去重后分配稳定 ID；自定义 FNV 哈希桶 + 负载因子扩容；冻结后只读；`Snapshot` 返回独立副本；字符串按字节精确存储（不归一化大小写或 UTF-8）；对 protoc-gen-go 风格的 JSON 名下划线转换做表驱动校验。

## 期望行为

1. 相同字符串重复 Intern 返回同一 ID，ID 按首次出现顺序从 0 递增，ByID 还原原文；空字符串可占 ID 0；
2. `Snapshot` 长度固定，后续插入不影响快照，修改快照不影响表；
3. 冻结后已有字符串仍可 Intern/Lookup，新字符串返回 `ErrFrozen` 且不插入；
4. 含 NUL、非法 UTF-8、大小写不同、Unicode 的字符串按字节精确区分；64KiB 长字符串可存取，近似长串不会误命中；
5. 容量 4 起始插入 4096 个符号后全部可查、ID 唯一、ByID 一致，扩容不丢条目；
6. JSON 名推导：`foo_bar→fooBar`、`foo__bar→fooBar`、`_foo→Foo`、`Foo_bar→FooBar`、`foo_bar_2→fooBar2`、`foo2_bar→foo2Bar`，已是驼峰的原样保留；
7. 相同顺序重建符号表得到完全一致的 ID；随机顺序重建得到 0..Len-1 的稠密唯一 ID，字符串映射仍正确；
8. 保留表/快照并丢弃原始名字切片，多轮 GC 后全部条目、长字符串与 ID 查询不变；
9. 500 个 `UAF_SEED` 随机字符串（含重复插入）去重与 ID 稳定性正确，冻结后新键被拒绝，GC 后仍可查询；
10. 最后一行 `ALL PASS l10_string_table`。

## 失败判读

- 重复字符串产生新 ID：哈希/桶查找失败或未做内容比较；
- 扩容后丢条目：rehash 未遍历旧桶；
- 近似长串误命中：只比较长度/哈希/前缀；
- 大小写或非法 UTF-8 被归一化：用了 strings.ToLower/Trim 等处理；
- 冻结后仍插入：冻结检查缺失或顺序错误（先插入后检查）；
- 快照随更新变化：快照返回了内部切片而非副本；
- GC 后崩溃/乱码：值未保存在表中；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机插入顺序种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_string_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
