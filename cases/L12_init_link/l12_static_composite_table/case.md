---
id: l12_static_composite_table
dim: L12_init_link
priority: P2
sources:
  - protobuf-go@v1.36.6: proto/merge_test.go:36 var testMerges = []testMerge{{ 大型静态复合字面量测试表
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/extensions/ext/ext.pb.go:389 生成代码的 []protoimpl.ExtensionInfo 静态表（含指针字段）
  - gitea@v1.23.5: modules/validation/refname_test.go:12 var gitRefNameValidationTestCases 静态表
tags: [L12, static-data, composite-literal, linker-relocation, gcprog, pointer-identity, map, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 1 轮全量校验；TORTURE_ROUNDS 放大到 16 轮）"
---

# 场景

`static_table.go` 用纯包级复合字面量定义 96 个 `*item`（即 `nodes`），每项含字符串、`[4]byte`、`int16` 与指向内联 `blob` 的指针；`byName`/`byID` 两张 map、8×12 的 `groups` 分组表与 24 项匿名 struct 切片都在字面量中引用 `nodes` 元素。表完全由链接期数据重定位生成，init 只抓取快照，不做构造。主程序全量校验结构，并在并发只读与多轮显式 GC 下确认指针身份不变。

## 期望行为

1. init 快照：`len(nodes)==96`、两张 map 各 96 项、`groups["g7"]` 12 项、`anon` 24 项，且 `byName["n000"]==nodes[0]`、`byID[95]==nodes[95]`；
2. 每项 id/name/weight 符合公式；tag 与 payload 字节分别符合 `(i*3+j)&0xff`、`(i*5+j)&0xff`；
3. `byName[nodeName(i)]`、`byID[i]` 与 `nodes[i]` 是同一指针；
4. `groups["g%d"]`（i%8）的第 `i/8` 个元素是 `nodes[i]`；
5. `anon[k].it == nodes[(k*11)%96]`，weight 与 label 符合公式；
6. 8 goroutine 并发读三张表并穿插显式 GC 时结果全部正确；
7. 每轮 `TORTURE_ROUNDS` 做全量校验 + 显式 GC，默认 1 轮、上限 16；
8. GC 后表长度、指针身份与 payload 不变；最后一行 `ALL PASS l12_static_composite_table`。

## 失败判读

- init 快照不完整：静态表未在包变量初始化阶段完成，或字面量元素丢失；
- map/分组/指针身份失败：链接期重定位或 map 字面量构造错误；
- payload/tag 字节错误：静态数据放置或写入错误；
- GC 后指针身份变化：指针字段的 gcprog 生成/扫描错误；
- 并发阶段失败：静态表被错误回收；
- 失败行 `seed=` 可用于复现并发采样起点与轮换。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 16）：全量校验 + 显式 GC 的轮数；
- `UAF_SEED`：决定并发采样起点。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_static_composite_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
