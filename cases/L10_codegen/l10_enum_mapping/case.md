---
id: l10_enum_mapping
dim: L10_codegen
priority: P0
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/enumprefix_test.go:13 TestStripEnumPrefix 枚举值命名与前缀剥离
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:67 TestName 枚举值名/JSON 名推导
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 + internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:12-65 枚举 String/Valid/AllValues 生成物
  - ent@v0.14.4: schema/field/field_test.go:748 TestField_Enums 枚举值表与默认值
tags: [L10, L06, enum, mapping-table, alias, json, text-marshal, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟生成器的枚举映射表：三个枚举（含 `allow_alias` 重值、负值/稀疏大值）各有声明顺序别名表、值→规范名 map、名→值反查 map、唯一值列表，并生成 String/Valid/MarshalText/UnmarshalText 与 JSON 编解码。验证别名首名获胜、未知值回退、文本/JSON 往返、全表 GC 稳定性以及随机反查。

## 期望行为

1. Status 6 个别名、5 个唯一值，`STATUS_ENABLED` 与 `STATUS_ACTIVE` 同值且规范名为先声明的 `STATUS_ACTIVE`；Mode 的 3 同理为 `MODE_RW`；
2. 每个合法值的 String/Valid/MarshalText/UnmarshalText 往返一致，别名输入归一到同值；值列表无重复且顺序固定；
3. 未知值的 String 回退为 `Kind(n)`，Valid=false，十进制文本可往返（含负值/极值）；
4. JSON 输出规范名；别名 JSON 输入解码为同值并重新输出规范名；未知数值串保留；大小写不符/未知名报错；
5. 保留名称串、解码结构体与 JSON 字节，多轮 GC 后全表校验和、视图与编码结果不变；
6. 4000 次 `UAF_SEED` 随机探测（名称反查、值命名、文本往返、JSON 往返）与线性独立模型一致，周期性 GC 后校验和不变；
7. `AllValues` 返回副本，修改返回值不影响内部表；
8. 最后一行 `ALL PASS l10_enum_mapping`。

## 失败判读

- 某值规范名错位：别名首名获胜逻辑或声明顺序错误；
- 未知值未回退：边界判断漏掉负数/上界；
- JSON 别名未归一：反查表未参与文本解码；
- GC 后校验和/字符串变化：名称常量或保留视图未保活（编译器问题）；
- 随机失败可凭 `seed=` 复现，按枚举/探测类型定位。

## 参数

- `UAF_SEED`：随机探测序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_enum_mapping/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
