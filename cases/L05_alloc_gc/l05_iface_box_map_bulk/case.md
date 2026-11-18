---
id: l05_iface_box_map_bulk
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: ast/api_native_test.go:132 的 TestStackAny：//go:noinline 把值装箱成 any，GC 后再断言/序列化
  - sonic@v1.13.2: issue_test/issue115_test.go:44 的 TestIssue115_MarshalMapWithSort：多种数值 map 键与极端值
tags: [L05, interface-boxing, map-churn, type-switch, itab, gc]
requires: []
runtime: "<10s"
---

# 场景

`map[int64]any` 一次写入 16000 个六类装箱值：int64、string、[]byte、按值
160 字节大 struct（含内部指针）、`*node` 与捕获 val 的闭包。建表后 GC 并逐个
type switch 还原；再覆盖 1/4、删除 1/5，GC 后校验更新语义；整表丢弃后仅靠
pin 与覆盖前的旧接口值跨 GC，最后重建新表并检查 typed-nil 接口语义。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_iface_box_map_bulk`；
2. 建表后长度恰为 16000，六类装箱值的动态类型与内容全部正确；
3. 按值大 struct 的字段、内部指针身份与 payload 在 GC 后不变；
4. `*node` 与闭包路径保持指针身份和返回值，map bucket 扫描不丢接口数据字；
5. 覆盖项为指定 int64/string，删除项不存在，其余项保持原类型与内容；
6. 被覆盖前的旧接口值、pin 的接口值在整表丢弃 + GC 后仍能还原；
7. 重建的新 map 与旧 pin 互不影响；
8. typed-nil `*ibNode` 装箱后接口非 nil，断言得到 nil 指针；
9. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `kindX not ...` / `i=... missing`：接口 type 指针/itab 或 map 槽位被破坏；
- `pointer identity lost`：指针装箱路径复制错误或底层对象被回收；
- `big inner pointer lost` / `big fixed byte`：按值大 struct 装箱副本字段丢失；
- `closure=...`：func 值/闭包环境在 GC 中损坏；
- `old-any after drop+GC` / `pin after drop+GC`：仍被接口引用的数据被提前回收（UAF）；
- `typed-nil`：接口 typed-nil 语义错误。

## 参数

- `UAF_SEED`：决定每个 key 的装箱类型、payload 长度与内容（默认 1，解析失败回退 1），失败输出带种子；
- 无 TORTURE 参数；固定 16000 项。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_iface_box_map_bulk/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
