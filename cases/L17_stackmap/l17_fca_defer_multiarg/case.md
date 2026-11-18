---
id: l17_fca_defer_multiarg
dim: L17_stackmap
priority: P1
sources:
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/struct_test.go:82（TestEncodeNestedStruct 自引用嵌套聚合）
  - hugo@v0.155.0: hugolib/rebuild_test.go:694（TestRebuildDeeplyNestedLink 深层嵌套结构遍历）
tags: [L17, stackmap, FCA, nested-aggregate, multiarg-defer, byval-copy, any-field, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`bundle` 由 `mat2{[2][2]leaf, any}`、`[3]*item`、`[]*item` 与 string 组成，属嵌套 FCA 聚合。一轮中创建两个 bundle 与一个 `[2]leaf` 数组，分别注册为普通 defer 实参与闭包 defer 实参（整体按值复制）。注册后原地重绑 b1 的嵌套 leaf 指针、any、ptrs、list 与 tag，并替换局部 leaf 数组元素；defer 内两轮 GC 后必须仍看到注册时的副本。函数最后按值返回被修改的 bundle，caller GC 后校验。另有一条直接传参路径。

`UAF_SEED` 决定每轮 base 与 nil/非 nil list、any 形态。

# 期望行为

1. 两条 defer 路径的 checksum 均等于原始 b1/b2/leaf 数组之和；
2. defer 中的嵌套聚合副本不受注册后局部字段重绑影响；
3. 返回的 bundle 携带 mutation 后的嵌套指针、tag、list 与新 appended 元素；
4. caller 在 GC 后重算返回 bundle checksum，与函数内记录一致；
5. 直接 `takeTwo(makeBundle(...), ...)` 路径 checksum 正确；
6. 退出码 0，最后一行 `ALL PASS l17_fca_defer_multiarg`。

# 失败判读

- `direct/closure FCA defer sum`：多参数 FCA 的 defer 记录未被完整扫描；
- `returned bundle sum`：按值返回槽中的嵌套聚合被破坏；
- `returned nested pointer/scalar`：FCA 字段拷贝或返回不完整；
- `mutation did not change`：用例自身构造错误；
- 仅 clobberfree 下失败：defer 记录/返回槽中的聚合指针漏标。

# 参数

- `UAF_SEED`：每轮 base 与形态种子，失败输出带该值；
- 固定 6 轮，list/any 在 nil 与指针/字符串间轮换。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_fca_defer_multiarg/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
