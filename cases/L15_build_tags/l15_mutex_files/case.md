---
id: l15_mutex_files
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_mutex_files/variant_base.go:1 默认互斥文件
  - cases/L15_build_tags/l15_mutex_files/variant_red.go:1 tagRed 互斥文件
  - cases/L15_build_tags/l15_mutex_files/variant_both.go:1 双 tag 互斥文件
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, mutual-exclusion, file-selection, init]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

四个文件分别覆盖 `tagRed`/`tagBlue` 的四种组合且两两互斥：base（无 tag）、red（仅 tagRed）、blue（仅 tagBlue）、both（两者都有），都定义同名 `variantImpl`。构建成功即证明约束没有交叠；每个文件的 `init` 再向 `variantInits` 追加标记，运行期确认恰好只有一个实现被编译。

## 期望行为

1. 默认构建选中 `"base"`；`-tags=tagRed`/`tagBlue`/`tagRed,tagBlue` 分别选中 `"red"`/`"blue"`/`"both"`；
2. `len(variantInits)==1` 且标记与实现一致（排除“多个文件同时编译但符号名不同”的漏网场景）；
3. GC 后实现值与 payload 指针不变；
4. 退出码 0，最后一行 `ALL PASS l15_mutex_files`。

## 失败判读

- 构建失败（重复定义）：互斥约束交叠；
- init 标记数量 != 1：文件选择集合错误；
- 默认或带 tag 选择错误：`&&`/`!` 组合解析错误。

## 参数

- `EXPECT_IMPL=base|red|blue|both`：默认 base；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_mutex_files
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagRed,tagBlue -o /tmp/case-b . && EXPECT_IMPL=both /tmp/case-b
```
