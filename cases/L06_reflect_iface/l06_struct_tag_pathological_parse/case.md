---
id: l06_struct_tag_pathological_parse
dim: L06_reflect_iface
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/encoding/tag/tag_test.go:29 tag 值的转义字节序列文法
  - ent@v0.14.4: entc/gen/graph_test.go:548 ensureStructTag 用 StructTag.Lookup 校验字段 tag
  - gin@v1.10.0: binding/default_validator_test.go:49 binding tag 的 max/gt 选项解析
  - echo@v4.13.4: bind_test.go:319 嵌套 struct 字段的 query tag 绑定
tags: [L06, reflect, struct-tag, StructTag, Lookup, malformed-tag, escaping, tag-options, StructOf, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

配置/绑定/代码生成依赖 `reflect.StructTag` 解析 `json`/`db`/`binding` 等 tag。除正常文法外，
线上配置会混入手写错误：制表符或换行当分隔符、未加引号、未闭合引号、空 key、非法转义、DEL
控制字符、key 含空格。本用例构造 19 个字段的病态 tag 矩阵，逐项校验 `Lookup/Get` 的边界语义、
基于 tag 的确定性绑定计划，以及 `StructOf` 原样保存 raw tag 后跨 GC 的读取。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_struct_tag_pathological_parse`；
2. 合法文法：`json:"good,omitempty"` 的选项可拆；引号内逗号保留；`\"`、`\\`、`\t` 正确解码；
   key 可含 `-`、`_`、数字、`.`，后续 key 仍可读；
3. `json:""` 与 key 缺失可区分；空 tag 的 Lookup 恒失败；前导/尾随空格合法，多个空格可分隔 pair；
4. 尾部垃圾不影响已解析键；非法转义使该 key 不可见但后续 well-formed pair 仍可读；
5. 未加引号、未闭合引号、空 key 会终止整个 tag 解析；tab/newline 不是合法 separator，DEL/空格
   出现在 key 中会终止解析；
6. 绑定计划仅含导出、非匿名、json 名非空且非 `-` 的字段，顺序与字段声明顺序一致；
7. `StructOf` 原样保存合法与病态 raw tag，Lookup 语义不变；反射写入的字段值与 tag 跨显式 GC 保持。

# 失败判读

- `tag options` / `escaped ...`：StructTag 的 qvalue 反解或选项拆分错误；
- `... hides all keys`：畸形 tag 未按语法错误终止，可能读到错位内容；
- `tab/newline is not a tag separator`：非法分隔符被误接受；
- `binding plan ...`：tag 过滤/名字解析错误；
- `StructOf preserves ...` / `... after GC`：tag 字符串未被原样保存或未保活；
- 崩溃：字段元数据偏移/tag 指针损坏。

# 参数

- `UAF_SEED`：反射写入字段值的后缀来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_struct_tag_pathological_parse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
