---
id: l17_fca_nested_mixed
dim: L17_stackmap
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（大消息 struct 的反射视图跨多轮 GC）
  - sonic@v1.13.2: decode_test.go:2433（TestUnmarshalMaxDepth 深层嵌套聚合）
tags: [L17, stackmap, FCA, nested-aggregate, any-field, func-field, map-field, byval-return, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 6 轮）"
---

# 场景

三层异构嵌套聚合 `top{ mid{ [2]leaf; *item; any }, [2][2]*item, func() *item, map[string]*item, string }`：

1. `makeTop` 构造后在返回前 GC；
2. `takeTop` 按值收下整个 top，内部两次 GC 并两遍 fold；
3. `takeMid` 按值收下 mid（含 `[2]leaf` 与 any 中的 leaf 值）；
4. `takeLeaves` 按值收下 `[2]leaf` 数组；
5. `retop` 按值收下 top、改写副本中的 `m.a[0].p` 与 `arr[1][1]` 后 GC 再按值返回；caller 验证副本已改、原值未变。

所有 fold 都经 interface type switch、func 调用与 map 查表读取指针字段。

## 期望行为

1. `takeTop` 两遍结果均为 `14*base+110`；
2. `takeMid == 7*base+38`，`takeLeaves == 4*base+14`；
3. `makeTop`/`takeTop` 后原 top 的 leaf/嵌套数组/map/func/any/string 全部正确；
4. `retop` 返回值中 `a[0].p.v==base+77`、`arr[1][1].v==base+99`、tail=="ret"，其余字段保持原值；原 top 未被修改；
5. `foldTop(retop result) == 14*base+161`（+76 +86 -1）；
6. 最后一行 `ALL PASS l17_fca_nested_mixed`，退出码 0。

## 失败判读

- `takeTop/takeMid/takeLeaves got` 偏差：嵌套聚合参数的 FCA 拆分/deopt 参数指针槽遗漏；
- `nested aggregate corrupted`：嵌套偏移处的指针未被扫描或返回槽未重定位；
- `foldTop(rt)` 偏差：按值返回的聚合副本中指针错位；
- `retop-original` 失败：按值语义被破坏（非 GC 问题）；
- 仅 clobberfree/efence 下失败：聚合内引用对象被提前回收；
- 崩溃：FCA 聚合参数/返回值的栈位图错误。

## 参数

- `UAF_SEED`：决定每轮 base，失败输出带种子；
- 无 TORTURE 参数，6 轮固定。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_fca_nested_mixed/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
