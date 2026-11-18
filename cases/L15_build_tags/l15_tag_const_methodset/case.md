---
id: l15_tag_const_methodset
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_const_methodset/mode_base.go:5 默认 tagMode 常量
  - cases/L15_build_tags/l15_tag_const_methodset/mode_base.go:7 默认方法集
  - cases/L15_build_tags/l15_tag_const_methodset/mode_ext.go:5 tagExt 常量
  - cases/L15_build_tags/l15_tag_const_methodset/mode_ext.go:9 tagExt 额外方法
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, constant, method-set, reflect, interface, custom-tags]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

同一个类型 `tagged` 的方法由不同 tag 文件提供：默认 `!tagExt` 只有 `Base()` 且 `tagMode=="base"`；`-tags=tagExt` 时 `tagMode=="ext"` 并额外提供 `Extra()`。运行期用接口断言与反射方法集验证构建标签确实改变常量值与方法集。

## 期望行为

1. `tagMode` 等于 `EXPECT_TAG`（默认 `base`，ext 构建为 `ext`）；
2. `Base()` 返回构建对应的字符串；`tagged` 始终满足 `baseIface`；
3. `any(t).(extraIface)` 断言仅在 ext 构建成立；
4. 反射得到的值方法集为 `[Base]`（base）或 `[Base Extra]`（ext），数量分别为 1/2；
5. GC 后 `tagged` 的 `n` 与 payload 指针不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_const_methodset`。

## 失败判读

- 常量或方法体不匹配：构建标签选择错误；
- 默认构建却满足 Extra 接口/方法数为 2：互斥约束失效（构建可能同时包含两个文件时会直接重复定义失败）；
- 反射方法集与接口断言不一致：方法集元数据/链接错误。

## 参数

- `EXPECT_TAG=base|ext`：默认 base；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_const_methodset
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagExt -o /tmp/case-ext . && EXPECT_TAG=ext /tmp/case-ext
```
