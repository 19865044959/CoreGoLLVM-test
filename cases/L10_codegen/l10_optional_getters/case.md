---
id: l10_optional_getters
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:150 TestOpaqueGet 生成消息访问器与零值语义
  - protobuf-go@v1.36.6: internal/encoding/defval/default_test.go:17 标量/bytes 默认值格式化
  - protobuf-go@v1.36.6: proto/reset_test.go:15 TestReset 生成消息清空后复用
tags: [L10, L06, optional, getter, presence, default-table, nil-receiver, method-value, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 protoc-gen-go optional 字段的生成方法集：标量字段用指针表示 presence，blob 用切片 nil 表示未设置，生成 `Get*`（未设置返回编译期默认值）与 `Has*`。默认值包括 int32/int64/float64/bool/string/enum/bytes，并生成字段描述符表（名称/类型/默认值）覆盖全部 8 个字段。覆盖零值、显式零值、nil receiver、方法值、GC 后读取与随机 presence 组合。

## 期望行为

1. 零值与 nil receiver 的所有 getter 返回默认值，所有 Has 为 false；`GetBlob` 返回非 nil 默认字节；
2. 显式设置零值（0/""/false/0.0/枚举 0/空非 nil blob/空 child）后 Has 为 true，getter 返回零值而非默认值；
3. 非零消息 GC 后标量、字符串、blob、嵌套 child 全部可读；
4. 字段描述符表长度 = struct 字段数 = 8，字段名逐一对应，Def 字符串与运行期默认值一致（含 blob 十六进制）；
5. 默认值在 40 轮垃圾分配与 GC 后保持稳定；
6. 方法值捕获指针 receiver/nil receiver，GC 后调用仍返回正确值；
7. 300 组 `UAF_SEED` 随机 presence 组合（absent/explicit-zero/nonzero）逐字段与独立期望模型一致，GC 后保留消息复核一致；
8. 最后一行 `ALL PASS l10_optional_getters`。

## 失败判读

- Has 与 Get 不一致：presence 指针未按 optional 语义维护；
- 显式零值返回默认值：getter 用值比较而非指针判空；
- nil receiver panic：getter/Has 缺少 receiver 判空；
- GC 后乱码/崩溃：指针字段/切片/child 未保活；
- 描述符表不符：生成器字段表漏项或默认值格式化错误；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机 presence/取值组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_optional_getters/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
