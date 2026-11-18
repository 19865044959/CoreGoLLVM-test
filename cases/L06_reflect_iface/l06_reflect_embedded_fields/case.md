---
id: l06_reflect_embedded_fields
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/schema_test.go:202 匿名嵌入 struct + embeddedPrefix 的字段展开
  - gorm@v1.30.0: schema/schema_test.go:181 三层嵌套模型的 BindNames 字段路径
  - gorm@v1.30.0: schema/index_test.go:58 跨 3 层嵌入合并复合索引字段
  - caddy@v2.10.0: context_test.go:22 struct tag 反射定位字段并做接口断言
tags: [L06, reflect, embedded-field, promotion, shadowing, FieldByIndex, embedded-interface, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

构造 `Top → Mid → Base` 的多层匿名嵌入（含 `*Leaf` 嵌入指针、嵌入 `Labeler` 接口与未导出字段），
用 `FieldByName/FieldByIndex` 读取和写入提升字段，验证外层字段遮蔽嵌入字段、未导出字段只能按
Kind 读取、`FieldByIndexErr` 对 nil 嵌入指针返回错误、`FieldByIndex` 直接 panic。丢弃类型化根后
仅由接口保活，GC 后沿相同 Index 路径与提升方法回读。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_embedded_fields`；
2. `FieldByName` 的 Index 路径：ID=`[0,0,0]`、hidden=`[0,2]`、Level=`[1,0]`、Shared=`[3]`；
3. 通过 `FieldByIndex` 写入提升字段，类型化视图与反射视图一致；外层 Shared 遮蔽嵌入 Shared；
4. 未导出字段 `CanInterface/CanSet` 均为 false，设置必须 panic 且被 recover；
5. nil 嵌入指针上 `FieldByIndexErr` 返回错误，`FieldByIndex` 必须 panic；
6. 嵌入接口方法与提升的值接收者方法可经 `MethodByName` 调用；
7. GC 后提升标量、嵌入指针、Tag 指针与嵌入接口全部保持，Index 路径不变。

## 失败判读

- `promoted ... index path`：嵌入字段提升/遮蔽计算错误，检查编译器字段索引生成；
- `set promoted ... via FieldByIndex`：反射写入偏移错误，可能破坏相邻字段；
- `FieldByIndexErr should report nil embedded pointer`：nil 嵌入指针路径缺失；
- `embedded interface after GC` / `Tag pointer after GC`：接口字段或指针字段未被扫描。

## 参数

- `UAF_SEED`：基础字段值来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_embedded_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
