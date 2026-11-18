---
id: l15_arch_mutex
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_arch_mutex/arch_x86.go:1 amd64 || 386 互斥文件
  - cases/L15_build_tags/l15_arch_mutex/arch_arm64.go:1 arm64 互斥文件
  - cases/L15_build_tags/l15_arch_mutex/arch_other.go:1 其余 arch 兜底
  - cases/L15_build_tags/l15_arch_mutex/main.go:50 init 标记与实现校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOARCH, mutual-exclusion, init, file-selection, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

三个 define 同名 `archMutImpl` 的文件按 GOARCH 互斥：`amd64 || 386`、`arm64`、`!(amd64||386||arm64)`。构建成功即证明约束无交叠；每个文件的 init 再向 archInits 追加标记，运行期确认恰好只有一个文件被编译。

## 期望行为

1. 当前 GOARCH 对应实现：amd64/386 为 `x86`，arm64 为 `arm64`，其余为 `other`；
2. `len(archInits)==1` 且标记与实现一致；
3. GC 后选择结果与 payload 不变；
4. 退出码 0，最后一行 `ALL PASS l15_arch_mutex`。

## 失败判读

- 构建失败（重复定义）：互斥约束交叠；
- init 标记数量 != 1：文件选择集合错误；
- 实现与 GOARCH 不匹配：`||`/`!` 在 GOARCH 上的解析错误。

## 参数

`BUILD_MODE=module`；不依赖自定义 tag，任意 GOARCH 直接构建。

## 运行

```bash
cd cases/L15_build_tags/l15_arch_mutex
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
