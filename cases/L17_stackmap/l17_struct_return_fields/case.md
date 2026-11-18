---
id: l17_struct_return_fields
dim: L17_stackmap
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（TestUnsafeAssumptions：含指针字段的消息 struct 跨多轮 GC 保留）
  - sonic@v1.13.2: ast/api_native_test.go:132（栈对象装箱后 GC，再读其字段）
tags: [L17, stackmap, aggregate-return, struct-by-value, interface, method, GC]
requires: []
runtime: "<10s（默认 8 轮）"
---

# 场景

`record` 同时含 `*item`、`[]*item`、`[3]*item`、`string`、`map[string]*item`、`any`、`[]byte` 和内嵌指针 struct，体积远大于寄存器返回槽。`build` 在返回前两次 GC；`mutate` 按值收下整个 record、改写若干指针字段后再按值返回；`(*builder).make` 经 `maker` 接口返回同一大 struct。调用方每次接收后 GC 并逐字段校验。

# 期望行为

1. `build` 返回值的所有指针/切片/字符串/map/interface 字段在 GC 后与构造值一致；
2. `mutate` 的返回值字段（新 head/arr[2]/meta["x"]/extra）正确，未改写字段保持调用方原值；
3. 按值传参不改变调用方 `record`；接口方法返回的大 struct 跨 GC 完好；
4. 最后一行 `ALL PASS l17_struct_return_fields`，退出码 0。

# 失败判读

- `FAIL: ... record corrupted after GC`：大聚合返回槽或按值参数某处指针槽漏标；
- `FAIL: method keep pointer`：receiver 持有的 `keep` 在方法内 GC 时未被扫描；
- 仅 clobberfree/efence 下失败：返回值引用的对象被提前回收；
- 崩溃：聚合返回槽偏移/指针位图错误。

# 参数

- `UAF_SEED`：决定每轮 id 偏移；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_struct_return_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
