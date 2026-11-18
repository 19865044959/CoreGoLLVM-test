---
id: l10_field_validation
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/checkinit_test.go:19 TestCheckInitializedErrors required 字段递归检查与错误路径
  - ent@v0.14.4: schema/field/field_test.go:44-50 Default/Min/Max/Range 生成的校验器描述与数量
  - sqlc@v1.28.0: internal/config/config_test.go:26 TestBadConfigs 严格配置解码：缺失/未知字段报错
tags: [L10, validation, required, range, enum, repeated, map, deterministic-errors, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

手写生成风格 `Validate()`：14 个字段覆盖 required、长度、前缀、email 形态、整数/浮点范围、枚举白名单、repeated 数量与元素、map 数量与排序后的 key/value、嵌套消息、bytes 长度、optional 显式零/负值、跨字段 mode/text/num 规则。错误按字段声明序收集，map 内按 key 字典序，校验过程不修改消息。

## 期望行为

1. 合法消息与显式零 optional 无错误；nil receiver 返回 `["message: nil"]`；
2. 每个单字段非法的错误文本与短路规则精确匹配（空 ID 只报 required，长度错不报前缀错等）；
3. 多错误消息的错误序列严格为 `id, age, kind, tags[1], attrs[a], attrs[z], num`（字段声明序 + map key 排序）；
4. `mode=0` 时不检查 num；`mode=1` 空 text 报 required、超长报 length；`mode=2` num 越界报错；非法 mode 只报 mode；
5. Validate 前后消息 dump 不变（不排序原 map、不写回字段）；
6. 错误串/合法消息/optional 指针/嵌套 item 视图在 GC 后仍可读，再次校验结果一致；
7. 500 次 `UAF_SEED` 随机消息（含强制合法与强制非法样本）与独立模型逐字符串一致，周期性 GC 后复核；
8. 最后一行 `ALL PASS l10_field_validation`。

## 失败判读

- 错误顺序不定：map 遍历未排序或按 map 顺序追加；
- 错误重复/缺失：单字段短路规则与模型不一致；
- 校验改变消息：对原 map/slice 排序或写回默认值；
- GC 后错误文本变化：错误串未保活（编译器问题）或用例 model 状态错；
- 随机失败可凭 `seed=` 复现，比较 got/want 列表定位字段。

## 参数

- `UAF_SEED`：随机消息/字段组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_field_validation/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
