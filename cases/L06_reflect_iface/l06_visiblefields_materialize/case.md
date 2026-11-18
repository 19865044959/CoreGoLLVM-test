---
id: l06_visiblefields_materialize
dim: L06_reflect_iface
priority: P1
sources:
  - tailscale@v1.80.2: types/dnstype/dnstype_test.go:16 reflect.VisibleFields(reflect.TypeFor[Resolver]()) 枚举
  - gorm@v1.30.0: schema/schema_test.go:181 三层嵌套模型的完整字段路径
  - gorm@v1.30.0: schema/schema_test.go:202 embedded + embeddedPrefix 的嵌入字段展开
  - gorm@v1.30.0: schema/field_test.go:16 反射 FieldByName/Convert/Set 写入字段
tags: [L06, reflect, VisibleFields, FieldByIndexErr, embedded-pointer, nil-chain, tag-driven-bind, materialize, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

从配置/表单装载层级结构时，框架先按 `VisibleFields` 得到字段路径，再沿 `Index` 写入；
嵌入指针为 nil 时需要先物化中间对象。gorm 的嵌套模型/嵌入前缀与 tailscale 的字段枚举都依赖
这一机制。本用例对 `Doc → *Detail → *Base` 的 nil 指针链枚举可见字段，按 `bind` tag 选出
叶子字段，用 `FieldByIndexErr` 检测并分配中间指针，写入后回读，并跨 GC 复核。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_visiblefields_materialize`；
2. `VisibleFields` 顺序为 `Detail,Base,ID,Note,Owner,hidden,Title,Skip,Extra`，
   各字段 Index 路径、Anonymous 标记与导出性正确；
3. `bind` tag 的读取正确；绑定计划仅含 `ID/Note/Owner/Title`，跳过匿名字段、未导出字段、
   无 tag 与 `-` tag；
4. 物化前深层 `FieldByIndexErr` 返回错误，`FieldByIndex`/`FieldByName` panic；浅层字段可访问；
5. 物化后 `Detail`/`Base` 指针被分配，四个字段写入正确，`Skip/Extra/hidden` 保持零值；
6. 物化后所有计划字段的 `FieldByIndexErr` 与 `FieldByName` 一致，嵌套指针链跨 GC 保持；
7. 将中间的 `Base` 置 nil 后深层路径重新报错，而 `Owner` 浅层路径仍可读。

## 失败判读

- `visible fields of Doc` / `visible index path`：VisibleFields 展开顺序或 Index 路径错误；
- `deep field ... should error` / `should succeed`：nil 嵌入指针的错误/成功路径判定错误；
- `ensure field ...`：物化时分配指针或字段可设置性失败；
- `ID bound` / `Note bound` / ...：按 tag 写值错误；
- `... after GC`：嵌入指针链或字段值未被扫描。

## 参数

- `UAF_SEED`：每轮写入的基值来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_visiblefields_materialize/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
