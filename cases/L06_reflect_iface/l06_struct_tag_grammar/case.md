---
id: l06_struct_tag_grammar
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: context_test.go:22 struct tag 反射定位 json.RawMessage 字段并实例化模块
  - echo@v4.13.4: bind_test.go:177 嵌套结构体的 tag 字段绑定
  - protobuf-go@v1.36.6: internal/encoding/tag/tag_test.go:18 protobuf struct tag 的 Marshal/Unmarshal 文法
  - ent@v0.14.4: entc/gen/graph_test.go:565 reflect.StructTag 校验每个字段 tag
  - gin@v1.10.0: binding/default_validator_test.go:49 tag 参数（max/gt）驱动的反射校验
tags: [L06, reflect, struct-tag, StructTag, Lookup, tag-options, escaped-quote, embedded-field, unexported-field, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

配置加载、参数绑定与代码生成都依赖 `reflect.StructTag` 读取 `json`/`db`/`gorm`/`form` 等 tag，
需要正确处理缺省值、空值、畸形/重复 tag、逗号选项、转义引号与嵌入/未导出字段。本用例构造
包含 11 个字段的 tag 矩阵，逐项校验 `Get`/`Lookup` 与选项解析，并通过 `VisibleFields`
读取嵌入字段与未导出字段的 tag，在 GC 后复核字段元数据。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_struct_tag_grammar`；
2. 字段顺序、`Index`、导出性、PkgPath、Offset 对齐符合预期；
3. `Lookup` 能区分 key 缺失与 `json:""` 的空值；畸形（无值/单引号/未闭合）、重复 key
   （首个生效）与前后空格的解析符合 `StructTag` 规则；
4. `json:"a,b"` 的带引号逗号保留在值中，`\"` 转义解码为 `"`；逗号选项与分号选项可稳定拆分；
5. `VisibleFields` 顺序为 `Embedded,Promoted,unexp,Extra`，提升字段 Index 为 `[0,0]`，
   未导出字段可读 tag 且 `CanInterface` 为 false；
6. 按“非匿名、已导出、tag 非空且非 `-`”生成的绑定计划为 `Promoted,Extra`；
7. 字段值、tag 字符串与 StructField 元数据跨显式 GC 保持一致。

## 失败判读

- `tag lookup ...`：`Get`/`Lookup` 对存在/缺失/空值/畸形 tag 的判定错误；
- `escaped quote decoded` / `quoted comma ...`：tag 值反引号/转义解析错误；
- `visible field order` / `promoted field tag and index`：嵌入提升索引计算错误；
- `unexported field tag readable`：未导出字段元数据读取被错误禁止；
- `... after GC`：tag 字符串或字段元数据未保活。

## 参数

- `UAF_SEED`：循环中写入的字段值来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_struct_tag_grammar/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
