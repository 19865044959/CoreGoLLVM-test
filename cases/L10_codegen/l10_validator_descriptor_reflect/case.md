---
id: l10_validator_descriptor_reflect
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/validate_test.go:20/43 生成消息 Valid/Invalid 分类
  - protobuf-go@v1.36.6: proto/checkinit_test.go:19 required 字段递归检查
  - ent@v0.14.4: schema/field/field_test.go:44 Default/Min/Max 生成的校验描述
  - sqlc@v1.28.0: internal/endtoend/vet_test.go:31 对生成代码执行 go/parser 静态检查
  - kitex@v0.13.0: tool/internal_pkg/tpl/pbtpl/pbtpl_test.go:25 模板渲染后交 go/format/go/parser 校验
tags: [L10, L16, goast, go-parser, reflect, validator, required, utf8, rune-length, enum, oneof, nested, cycle, depth, map-slice, gc, UAF_SEED]
requires: [goast]
runtime: "<10s（UAF_SEED 驱动随机单字段变异）"
---

# 场景

生成式校验器的另一条实现路径：用 `go/parser` 读取生成器风格的 schema 源码（`json` + `validate` struct tag），构建字段计划（规则按 tag 顺序保存），再用反射校验任意消息值。规则覆盖 required/presence、整数 min/max/enum、字符串 minlen/maxlen（**按 rune 计数**）/utf8/oneof、浮点 finite/min/max、`[]string` maxitems/eachminlen、`[]float64` eachfinite/eachmax、`map[string]int64` maxitems/keyprefix/valmin/valmax、嵌套消息递归（深度上限 8、指针环检测）。错误路径按字段声明序、规则序、map 键排序、slice 下标升序确定。

## 期望行为

1. 从源码构建 9 字段 Account 与 3 字段 Child（含自引用 `*Child`）计划，嵌套与 JSON 名正确；
2. 合法消息零违规；
3. 全违规消息产出 15 条按确定顺序排列的错误（含 tags[0]、limits[...] 排序、child.code、scores[...]）；
4. required 失败短路同字段后续规则（ID=0 只有 required，Name 为空只有 required）；
5. 多字节/4 字节 rune 按 rune 计数，超长才报 maxlen；非法 UTF-8 单独报 utf8；
6. nil optional 指针跳过规则；
7. 自引用 child 报 `cycle`，12 层链报 `depth` 且仅一条；
8. map 违规按 key 字典序产生（keyprefix 先于 valmin/valmax）；
9. 19 类 schema 构建错误（语法、缺 tag、重复 JSON、未导出/嵌入、未知类型/规则、min>max、minlen>maxlen、坏参数、嵌套误用等）全部拒绝；
10. 计划与值在 GC 后仍可校验；`UAF_SEED` 随机单字段变异 300 轮均只产生预期的规则错误。

## 失败判读

- `FAIL: ...`：规则解析、反射字段映射或错误顺序错误；失败信息会打印实际 `path:code` 序列；
- `deterministic violation order` 失败：字段/规则/排序遍历不确定；
- `rune` 相关失败：使用了字节长度而非 `utf8.RuneCountInString`；
- `cycle` / `depth` 失败：递归遍历缺少指针身份集合或深度上限；
- 构建错误未拒绝：parser/tag 校验遗漏；
- `SIGSEGV` / `found bad pointer`：反射值/计划在 GC 后失效或指针解引用越界。

## 参数

- `UAF_SEED`：随机变异选择种子，失败输出带该值。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_validator_descriptor_reflect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
