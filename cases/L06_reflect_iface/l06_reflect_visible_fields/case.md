---
id: l06_reflect_visible_fields
dim: L06_reflect_iface
priority: P1
sources:
  - tailscale@v1.80.2: types/dnstype/dnstype_test.go:16 reflect.VisibleFields(reflect.TypeFor[Resolver]()) 枚举
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:22 reflect 枚举全部 struct 字段防止新增字段漏判
  - gorm@v1.30.0: schema/schema_test.go:202 匿名嵌入字段的提升与前缀展开
tags: [L06, reflect, VisibleFields, embedded-field, shadowing, ambiguity, FieldByIndex, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

对 `Top → Mid → BaseA` + 直接嵌入 `*BaseB` + 嵌入 `Labeler` 接口的层级类型调用
`reflect.VisibleFields`，逐项校验输出顺序、Index 路径、Anonymous 与导出性；验证外层字段遮蔽、
较浅层 ID 遮蔽较深层 ID、同深度 ID 歧义时字段不可见，以及 `FieldByName` 与可见路径一致。
再用 nil 嵌入指针、非 struct 类型 panic 与 GC 后按 Index 路径回读。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_visible_fields`；
2. 10 个可见字段的名称与 Index 路径完全确定，包含匿名字段自身与被提升字段；
3. Anonymous 标记与类型正确（包含 `*BaseB` 与接口字段 Labeler）；
4. 每个可见字段的 `FieldByName` 路径与 VisibleFields 一致；外层 Shared 遮蔽嵌入 Shared；
5. 同深度歧义的 `Amb.ID` 不出现在 VisibleFields，`FieldByName("ID")` 失败；
6. nil 嵌入指针不影响 VisibleFields；按路径访问返回错误，`FieldByIndex` 必须 panic；
7. GC 后沿可见路径读取的标量、字符串、未导出字段与嵌入接口全部正确。

## 失败判读

- `visible field order ...` / `visible index path ...`：提升/遮蔽/遍历顺序计算错误；
- `FieldByName matches visible ...`：FieldByName 与 VisibleFields 不一致，字段索引错误；
- `ambiguous ID omitted ...`：同深度歧义未被抑制；
- `... after GC`：嵌入指针或接口字段未被扫描。

## 参数

- `UAF_SEED`：基础字段值来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_visible_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
