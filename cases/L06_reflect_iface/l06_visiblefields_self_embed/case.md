---
id: l06_visiblefields_self_embed
dim: L06_reflect_iface
priority: P1
sources:
  - tailscale@v1.80.2: types/dnstype/dnstype_test.go:16 reflect.VisibleFields 枚举解析器字段
  - gorm@v1.30.0: schema/schema_test.go:202 匿名嵌入 struct 的字段提升与前缀展开
  - gorm@v1.30.0: schema/relationship_test.go:96 自引用模型（Manager *User）的字段路径
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:22 反射枚举全部字段防止新增字段漏判
tags: [L06, reflect, VisibleFields, self-embedding, promotion, shadowing, ambiguity, FieldByIndexErr, embedded-interface, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

字段枚举/绑定层直接依赖 `reflect.VisibleFields` 的输出顺序与 Index 路径。除普通多层嵌入外，
自引用模型 `*loop` 嵌入自身（递归模型）与同深度歧义字段是边界。本用例对自引用 `loop`、
`outer{*loop;Name}`、双嵌入歧义 `ambiguous`、嵌入接口 `withIface` 调用 VisibleFields，
校验只展开一层、提升字段路径、遮蔽、歧义抑制与 FieldByName 一致性，并跨 GC 重读。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_visiblefields_self_embed`；
2. `VisibleFields(loop)` 恰为 `loop [0]`（匿名 `*loop`）与 `V [1]`，不会递归展开；
3. `VisibleFields(outer)` 为 `loop [0]`、`V [0,1]`、`Name [1]`，每个可见字段的
   `FieldByName` 返回同一 Index 路径；
4. `ambiguous{dup1;dup2}` 只列两个匿名字段，同深度 `X` 不可见且 `FieldByName("X")` 失败；
5. 嵌入未导出接口字段的 Name/Type/Anonymous/PkgPath 正确，后续字段 Index 为 [1]；
6. 对指针类型与非 struct 类型调用 VisibleFields 均 panic 并被 recover；
7. 由 VisibleFields 生成的 bind 计划确定：outer 为 `V@[0 1]=v, Name@[1]=name`，loop 仅 V；
8. 自引用嵌套值中顶层 V 遮蔽深层 V，深层路径可经 `FieldByIndexErr` 读取；nil 嵌入指针
   路径返回错误且非 Err 版本 panic；GC 后 root、深层指针、VisibleFields/tag 全部不变。

# 失败判读

- `self-embedded VisibleFields length` / `... promoted field`：自引用嵌入被递归展开或字段索引错误；
- `FieldByName matches VisibleFields`：字段提升/遮蔽索引与枚举不一致；
- `ambiguous X ...`：同深度歧义未被抑制；
- `embedded interface field`：匿名接口字段的 Name/PkgPath/Type 错误；
- `VisibleFields on pointer/non-struct panics` 缺失：参数校验缺失；
- `nested self ... after GC`：嵌入指针或字段值未被扫描。

# 参数

- `UAF_SEED`：字段基值与标签值来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_visiblefields_self_embed/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
