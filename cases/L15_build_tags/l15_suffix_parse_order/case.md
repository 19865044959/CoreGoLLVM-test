---
id: l15_suffix_parse_order
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_suffix_parse_order/pair_amd64_linux.go:1 末段 GOOS 决定，中间 amd64 不参与
  - cases/L15_build_tags/l15_suffix_parse_order/pair_darwin_arm64.go:1 合法 GOOS_GOARCH 成对匹配
  - cases/L15_build_tags/l15_suffix_parse_order/combo_linux_windows.go:1 中间 linux 被忽略
  - cases/L15_build_tags/l15_suffix_parse_order/extra_linux_amd64_v2.go:1 末段非标识，普通文件
  - cases/L15_build_tags/l15_suffix_parse_order/look_x86_64.go:1 末段 64 非 GOARCH，普通文件
  - cases/L15_build_tags/l15_suffix_parse_order/main.go:52 期望文件集合
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, GOOS, GOARCH, file-selection, parse-order, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

文件名后缀只按末尾 1~2 段解析：末段是当前 GOOS/GOARCH 就按单段匹配；
只有倒数第二段是合法 GOOS 且末段是合法 GOARCH 时才按成对后缀要求两者同时
匹配；更早的段只是文件名主体。`pair_amd64_linux.go` 的 `amd64` 位于中间，
所以它在 linux/arm64 也参与编译；`combo_linux_windows.go` 同理只在 windows
参与；而 `pair_darwin_arm64.go` 必须 darwin+arm64 同时成立。

## 期望行为

1. 任何平台都选中 `extra_linux_amd64_v2`、`look_x86_64`；
2. linux 另选 `last_linux`、`pair_amd64_linux`（任意 GOARCH）；
3. windows 另选 `last_windows`、`combo_linux_windows`，amd64 时再选 `pair_windows_amd64`；
4. darwin 另选 `pair_arm64_darwin`，arm64 时再选 `pair_darwin_arm64`；
5. 实际 init token 集合与上述期望排序后完全相等；
6. GC 后集合不变；
7. 退出码 0，最后一行 `ALL PASS l15_suffix_parse_order`。

## 失败判读

- 多出中间段命中的文件：实现错误地把所有 `_linux`/`_amd64` 段都当作过滤条件；
- 缺少成对后缀文件：GOOS_GOARCH 组合匹配过严或顺序解析错误；
- 多出 `pair_darwin_arm64`（linux/arm64）：只按末段 GOARCH 匹配，未要求成对；
- 集合错误但单段正确：后缀解析顺序与 go/build 不一致；
- 期望集合打印在 FAIL 行，可直接对照。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_suffix_parse_order
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=darwin GOARCH=arm64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.darwin .
```
