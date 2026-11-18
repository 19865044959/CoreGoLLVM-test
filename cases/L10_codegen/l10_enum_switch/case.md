---
id: l10_enum_switch
dim: L10_codegen
priority: P0
sources:
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 + internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:12-65 枚举 String/Valid/AllValues
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/enumprefix_test.go:13 枚举常量命名与前缀剥离
tags: [L10, L11, enum, stringer, switch-table, 500-values, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟代码生成器的 500 值枚举产物：500 个常量、名称表/反查 map，以及三张巨大 switch 表——`String()`、`colorValid`、`colorFromCode`，另有 10 组 `colorGroup` 分类 switch。验证全部取值与越界值的确定性映射、名称唯一性、GC 后全量查询结果不变，以及 `UAF_SEED` 随机访问（含负值/超上界）一致。

## 期望行为

1. 枚举声明 500 个值，名称表与反查 map 长度均为 500，名称无重复；
2. 0..499 全量满足：`String()=="COLOR_%04d"`、名称反查回原值、`colorFromCode` 回原值、`colorValid==true`、`colorGroup==i/50`；
3. 越界值（负数、500+、MinInt32/MaxInt32）`String()` 输出 `Color(n)`，Valid=false，两张映射均失败，group=-1；
4. 畸形名称（空前缀、大小写不符、超长数字、非数字）反查失败；
5. GC 后全量查询 checksum 与预计算值一致；
6. 2000 次 `UAF_SEED` 随机访问（有效/越界混合）全部通过，周期性 GC，保留名称串 GC 后仍可反查；
7. 最后一行 `ALL PASS l10_enum_switch`。

## 失败判读

- 某个具体值映射错：switch case 值与常量表挪位（生成器 bug 的典型形态）；
- 越界未走 default：switch 缺少 default 或 Valid 边界判断错误；
- GC 后结果变化：名称表被寄存器/常量折叠破坏属编译器问题，映射错属用例 bug；
- 名称重复：常量表生成去重逻辑错误；
- 退出码 1：任一 `ck` 失败。

## 参数

- `UAF_SEED`：随机访问序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_enum_switch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
