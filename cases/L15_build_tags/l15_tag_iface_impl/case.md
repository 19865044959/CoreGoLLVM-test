---
id: l15_tag_iface_impl
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_iface_impl/impl_base.go:1 默认 !tagAlt 实现
  - cases/L15_build_tags/l15_tag_iface_impl/impl_alt.go:1 tagAlt 实现
  - cases/L15_build_tags/l15_tag_iface_impl/speaker.go:3 Speaker 接口
  - cases/L15_build_tags/l15_tag_iface_impl/main.go:45 接口分派与反射校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, interface, implementation-selection, reflection, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

同一个 `Speaker` 接口由构建标签选择实现：默认 `!tagAlt` 编译 `baseSpeaker`，`-tags=tagAlt` 编译 `altSpeaker`。两者都提供 `Name/Say`，main 通过接口分派与反射具体类型确认实际编译进来的实现。

## 期望行为

1. `newSpeaker(msg).Name()` 等于期望实现名（默认 base，tagAlt 为 alt）；
2. 接口方法 `Say()` 返回对应实现的文本（`base:hello` / `alt:hello`）；
3. 反射得到的具体类型为 `baseSpeaker` 或 `altSpeaker`；
4. GC 后接口持有的 Message 指针及 next 链有效；
5. 退出码 0，最后一行 `ALL PASS l15_tag_iface_impl`。

## 失败判读

- 默认构建选到 alt 或 tagAlt 构建选到 base：互补约束解析错误；
- 构建报重复定义：两个实现文件同时被编译；
- 反射类型与接口行为不一致：方法集/符号链接错误；
- GC 后 payload 损坏：接口数据指针栈映射问题。

## 参数

- `EXPECT_IMPL=alt`：仅在 `-tags=tagAlt` 构建时使用，默认 base；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_iface_impl
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagAlt -o /tmp/case-alt . && EXPECT_IMPL=alt /tmp/case-alt
```
