---
id: l10_default_fill
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/encoding/defval/default_test.go:17 Test 默认值格式化（bool/int/float/string/bytes 转义）
  - ent@v0.14.4: schema/field/field_test.go:44 TestInt Default(10)+Min/Max 生成的默认值与校验器描述
  - ent@v0.14.4: schema/field/field_test.go:748 TestField_Enums 枚举默认值
  - protobuf-go@v1.36.6: proto/reset_test.go:15 TestReset 生成消息清空后复用
tags: [L10, L16, goast, defaults, presence, recursive-fill, idempotent, gc, UAF_SEED]
requires: [goast]
runtime: "<10s"
---

# 场景

生成式默认值填充：`go/parser` 解析一份生成器风格 Go 源码，从 struct tag 提取标量/bytes/[]string/map 默认值并构建默认值表；`FillDefaults` 通过反射按字段顺序填充，但只填 nil 指针和 nil 容器（显式零值/空容器优先），并递归处理已存在的 Child/Leaf；`NewProfile` 先分配嵌套消息再填充。另拒绝坏默认值与非法源码。

## 期望行为

1. AST 表：类型顺序 `[Profile Child Leaf]`，字段数 11/3/1，Profile 有 8 个 default tag；int/float/bool/string/hex bytes/[]string/map 默认值解析正确；消息指针与无 tag 容器无默认；
2. 语法错误、重复类型、重复字段、非法 int/float/bool/hex/字符串切片/map 默认值、message 指针上的 default、空切片元素均使构建失败；
3. 零值 Profile 填充后标量/bytes/slice/map 得到默认值；Child、无 tag 的 EmptyMap/EmptySlice 保持 nil；
4. `NewProfile` 分配 Child+Leaf 并递归填入 child/depth/leaf 默认值；
5. 显式零指针、空非 nil 容器、显式空 Child.Name 保留；已存在但字段为 nil 的 Child 被填充；nil Leaf 不由 FillDefaults 分配；无 tag 的空容器保留；
6. FillDefaults 幂等；nil receiver 安全；类型不匹配/nil 目标的 `fillWith` 返回错误；
7. 默认指针/bytes/slice/map/嵌套 Leaf 视图与 AST 表在多轮 GC 后稳定；
8. 240 个 `UAF_SEED` 随机 presence 组合与直接赋值模型逐字段一致，二次填充幂等，周期性 GC 后复核；
9. 最后一行 `ALL PASS l10_default_fill`。

## 失败判读

- 默认表提取错：struct tag 解析/类型分类错误；
- 显式零被覆盖：用值比较而非指针/容器 nil 判断；
- 空容器变 nil 或默认值：未区分 nil 与非 nil 空；
- 嵌套递归漏填/误分配：FillDefaults 只应填充已存在消息；
- 非法默认值未被拒绝：解析校验缺失；
- GC 后默认值丢失：默认表/填充结果未保活；
- 随机失败可凭 `seed=` 复现；
- `requires: [goast]`：无 go/parser、go/types 的构建可用 `CAPS` 跳过本用例。

## 参数

- `UAF_SEED`：随机 presence/取值组合种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_default_fill/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
