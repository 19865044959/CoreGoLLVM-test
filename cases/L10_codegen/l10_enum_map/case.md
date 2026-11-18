---
id: l10_enum_map
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/enum_test.go:19 枚举值经 Type().New 在值域 map 中映射
  - ent@v0.14.4: schema/field/field_test.go:748 枚举 Descriptor 的值/名称表与默认值
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:55 Valid() 枚举成员集合语义
  - sqlc@v1.28.0: internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:65 AllIPProtocolValues 去重后的值列表
tags: [L10, L13, enum-map, alias, canonical, copy-on-write, set-algebra, map-growth, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

生成代码的枚举 map 家族：`map[Enum]string` canonical 名表（首次声明胜出）、`map[string]Enum` 反向表（包含别名）、`map[Enum]struct{}` 允许值集合、写时复制快照；对 4096 个合成枚举做 map 扩容、删除、重插与 GC；在随机 churn 中维护正反表一致性，并验证集合代数（并/交/差）的确定性。

## 期望行为

1. 同值别名只占一个 canonical 名；反向表包含全部名字且指向同一值；未知数字/名字查询返回零值、false；
2. 允许集合按枚举值去重（别名不重复），排序后的值列表确定；返回的列表是副本，修改不影响集合；
3. 快照（名表/反表/集合）与源隔离：源新增键不影响快照，修改快照不影响源；
4. 唯一值声明随机打乱后重建，canonical dump 与校验和一致；反向映射逐项一致；
5. 4096 项正反表全部可查且一一对应；删除一半后 Len 与成员正确，GC 后保持一致；快照保留删除前的全部条目；重插后恢复完整；
6. 集合并/交/差返回排序结果且对 nil 集合安全；重复写入同一枚举值不增长集合；
7. 保留名表/反表/允许集合并丢弃源 map，多轮 GC 后内容与校验和不变；
8. 500 次 `UAF_SEED` 随机正反表插入/删除与模型逐项一致，周期性 GC 后别名 canonical、反向别名与允许集合稳定；
9. 最后一行 `ALL PASS l10_enum_map`。

## 失败判读

- 别名产生两个 canonical 项：首次声明判断缺失；
- 反向表缺别名/同名多值：反向 map 键冲突覆盖；
- 快照随源更新变化：快照返回了原 map 别名；
- 删除后仍命中：正反表删除不同步；
- 重插后反向不一致：只恢复了一张表；
- 集合操作含重复或顺序漂移：结果未排序/未按值去重；
- GC 后崩溃/乱码：保留视图未持有数据；
- 随机失败可凭 seed= 复现。

## 参数

- `UAF_SEED`：随机 churn 与重建顺序种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_enum_map/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
