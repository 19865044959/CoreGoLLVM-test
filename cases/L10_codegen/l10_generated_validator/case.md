---
id: l10_generated_validator
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/validate_test.go:20/43 生成消息的 Valid/Invalid 校验分类
  - protobuf-go@v1.36.6: proto/checkinit_test.go:19 required 字段递归检查（嵌套/重复/map）
  - ent@v0.14.4: schema/field/field_test.go:44 Default/Min/Max 生成的校验器描述
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:55 Valid() 枚举成员校验
tags: [L10, L16, validator, rule-plan, spec-validation, violation-order, nested, oneof, cross-field, depth-limit, cycle, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

生成式校验器：从紧凑生成 spec（`message=<schema>` 分段 + `field|op|params`）构建规则计划，构建期校验字段/类型/参数/正则/nested 目标/跨字段目标/重复规则；运行时按字段顺序收集违规（稳定路径与 code 排序），覆盖 required、长度、整数/浮点范围、正则、前后缀/包含、枚举集合、repeated min/max/each、map min/max/key-prefix/val-range、嵌套消息、oneof required、跨字段 `start<=end`；递归 next 链带深度上限与环检测。

## 期望行为

1. 合法消息零违规；nil 顶层零违规；address 缺席不触发嵌套规则；typed-nil oneof 视为未设置；
2. 全违规组合返回 15 条确定违规，路径如 `address.city`、`labels[x]`、`tags[1]`，同路径按 code 排序；
3. 单条规则逐项命中：短/长 name、age 越界、score 越界、bad email、非法 role、tags 数量/元素长度、labels 数量/键前缀/值范围、address 字段、start>end；
4. next 嵌套违规带 `next.` / `next.next.` 前缀，合法 next 不产生违规；
5. 12 层链在深度上限处产生一条 `max-depth`（路径为 9 段 next）；自环在 `next`、双节点环在 `next.next` 各产生一条 `cycle`；
6. 计划构建错误：未知 op/字段/消息、类型不匹配、非法整数/浮点/正则、min>max、负计数、空枚举、未知 nested 目标、未知/自引用 le_field、重复规则、重复 message 段、段前规则、oneof_required 用于非 oneof 均报错；
7. 保留计划与违规切片，丢弃源消息并多轮 GC 后校验结果与条数不变；
8. 合法基线随机化后叠加 1..4 个互不冲突的变异（共 27 种变异，覆盖 11 个字段组），200 条 `UAF_SEED` 用例的违规集合与期望精确一致，周期性 GC 后稳定；
9. 最后一行 `ALL PASS l10_generated_validator`。

## 失败判读

- 违规顺序漂移：未按 path/code 排序或直接遍历 map；
- 同一路径漏/多违规：规则应用 switch 未覆盖某 op；
- 嵌套路径缺前缀：递归时未拼接父路径；
- map 违规顺序不稳定：未排序 map 键；
- 深度/环检测死循环或漏报：seen 未按递归栈删除，或检查顺序错误；
- oneof typed-nil 未识别：只用 interface==nil 判断；
- 计划接受非法 spec：缺字段类型/参数/目标校验；
- 变异用例的期望与实现不一致时，先核对双方的规则语义（尤其是 required 与 strlen 同时触发）；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机变异组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_generated_validator/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
