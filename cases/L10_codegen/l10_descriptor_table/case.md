---
id: l10_descriptor_table
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/filedesc/desc_test.go:32 TestFile 由 FileDescriptorProto 构造字段/嵌套描述表
  - protobuf-go@v1.36.6: internal/filedesc/build_test.go:23 TestInit 描述符 init 注册与全字段覆盖
  - ent@v0.14.4: entc/gen/graph_test.go:68 TestNewGraph 节点/边描述与确定性排序
tags: [L10, L12, descriptor-table, flattening, path-lookup, offset-table, validation, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟生成代码的扁平 descriptor 表：紧凑字面量描述（message 嵌套、字段、enum/value）经校验后展开为 message/field/enum/value 四个切片，字段用 `[FieldStart,FieldEnd)` 偏移表，message/enum 以 parent 索引表达嵌套；另建 full name、字段号索引与 `sync.Once` 单例 accessor。验证偏移、路径重建、查找、确定性渲染、构建失败原子性与 GC。

## 期望行为

1. 合法表：4 message、14 field、2 enum、5 enum value；字段/枚举值偏移连续，parent 索引均小于自身，`path` 重建的 full name 与表项一致；
2. 按 full name 查 message/enum、按字段号查 field 均命中且返回内容正确；未知名称/字段号、负 message 索引返回失败；
3. 嵌套 message（`gen.v1.Root.Child.Leaf`）、嵌套 enum（`gen.v1.Root.State`）与 oneof 索引正确；重复/空容器标志正确；
4. 渲染输出按声明顺序、逐字节确定；`fileDescriptor()` 多次调用返回同一指针；
5. 构建后修改调用方 spec 不影响已建表（表拥有副本）；
6. 18 类非法描述符被拒绝且错误信息可定位：空 path/package/message/field/value 名、重复符号、未知 parent、重复字段号/JSON 名/字段名、未知 kind、未知/错配类型引用、空 enum、重复 enum value、oneof 越界；
7. GC 后单例指针、渲染串、字段描述副本与 full name 视图稳定；
8. 700 次 `UAF_SEED` 随机 message/enum/字段号/路径查找与模型一致，周期性 GC 后渲染不变；
9. 最后一行 `ALL PASS l10_descriptor_table`。

## 失败判读

- 偏移表错：FieldStart/End 或 ValueStart/End 计算错误；
- parent 链错：嵌套路径重建/校验未按声明序；
- 冲突未检出：message 与 enum 未共享符号命名空间；
- 部分提交：校验与建表未分离，失败后留下半成品；
- 渲染不稳定：遍历了 map 而非切片；
- GC 后变化：全局单例或描述符内容未保活；
- 随机失败可凭 `seed=` 复现。

## 参数

- `UAF_SEED`：随机查找序列种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_descriptor_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
