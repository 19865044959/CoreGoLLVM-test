---
id: l12_static_iface_gcprog
dim: L12_init_link
priority: P2
sources:
  - protobuf-go@v1.36.6: proto/merge_test.go:36 var testMerges = []testMerge{{...}} 大型静态复合表
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/extensions/ext/ext.pb.go:389 []protoimpl.ExtensionInfo 静态指针表
  - goccy/go-json@v0.10.5: stream_test.go:26 var streamTest = []interface{}{...} 静态 interface 表
  - fiber@v2.52.6: log/default_test.go:100 []interface{} 静态参数表
tags: [L12, static-data, composite-literal, interface-values, linker-relocation, gcprog, pointer-identity, func-value, map-literal, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 1 轮全量校验；TORTURE_ROUNDS 1..8 轮）"
---

# 场景

包级静态复合表混合 interface 值与指针：`entries` 32 项，每项含 `any`、`[4]byte`、指向 `nodes` 与 `blobs` 的指针；`byKey`/`byIndex` 为 map 字面量，`ptrs` 为指针数组，`ifaceSlice` 与 `funcs` 为 interface/函数值表。表全部由链接期数据重定位与包初始化构造，init 只抓快照；主程序对每个 interface 值做类型断言并验证指针身份。

## 期望行为

1. init 快照：nodes 16、entries 32、byKey 16，first/last 指针身份正确，`byKey["n03"].id==3`；
2. nodes[i] 的 id/name/weight 符合公式，blob 指向 `blobs[i]` 且字节为 `i*8+j`；
3. entries[i] 的 key/tag/ptr/blob 符合公式；前 16 项 `val` 为 `*item` 且与 `nodes[i]` 同一；
4. 字符串/int64/[]byte/map/custom/func/nil/*[8]byte 各类 interface 值类型与内容正确；
5. `byKey`/`byIndex`/`ptrs`/`ifaceSlice`/`funcs` 的指针身份、类型与调用结果正确；
6. 8 goroutine 并发读三张表并穿插显式 GC 时结果正确；
7. `TORTURE_ROUNDS`（默认 1，上限 8）重复全量结构校验 + 显式 GC；
8. GC 后表长度、指针身份、interface payload 与静态字节不变；
9. 最后一行 `ALL PASS l12_static_iface_gcprog`。

## 失败判读

- init 快照不完整：静态表未在包变量初始化阶段完成或字面量元素丢失；
- map/数组/指针身份错误：链接期重定位或复合字面量构造错误；
- interface 值类型/内容错误：接口数据字（type/itab/data）放置或 GC 扫描错误；
- 函数值不可调用：静态 funcval 重定位错误；
- GC 后指针身份变化：指针字段的 gcprog 生成/扫描错误；
- 并发阶段失败：静态表被错误回收；
- 失败行 `seed=` 可用于复现并发采样起点。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 8）：全量校验 + 显式 GC 的轮数；
- `UAF_SEED`：决定并发采样起点与 GC 轮数。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_static_iface_gcprog/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=7 /tmp/case
```
