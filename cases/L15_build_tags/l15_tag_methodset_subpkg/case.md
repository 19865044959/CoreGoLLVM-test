---
id: l15_tag_methodset_subpkg
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_methodset_subpkg/impl/extra_on.go:1 tagExtra 额外方法
  - cases/L15_build_tags/l15_tag_methodset_subpkg/impl/extra_off.go:1 默认无额外方法
  - cases/L15_build_tags/l15_tag_methodset_subpkg/impl/impl.go:10 Record 基础方法集
  - cases/L15_build_tags/l15_tag_methodset_subpkg/iface/iface.go:3 跨包接口定义
  - cases/L15_build_tags/l15_tag_methodset_subpkg/main.go:58 接口断言与反射方法集
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, method-set, interface, reflection, multi-package, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

子包 `impl` 中的 `Record` 始终提供 `Get`，`Extra` 方法只在 `-tags=tagExtra` 时由额外文件提供。main 通过 `iface` 包的 Value/Extra 接口断言、方法调用与反射指针方法集，验证方法集随构建标签变化。

## 期望行为

1. `Record` 始终满足 `iface.Value`，Get 返回 13；
2. 默认构建不满足 `iface.Extra`，tagExtra 构建满足且 Extra 返回 `extra:record`；
3. 反射指针方法集默认为 `[Get]`，tagExtra 构建为 `[Extra Get]`；
4. GC 后 Record 的 N/Node 指针及方法集不变；
5. 退出码 0，最后一行 `ALL PASS l15_tag_methodset_subpkg`。

## 失败判读

- 默认构建却满足 Extra 接口：互补约束失效或方法被错误链接；
- tagExtra 构建方法缺失：tag 文件名/约束错误；
- 反射方法集与接口断言不一致：方法集元数据错误；
- 构建失败：两个方法定义文件约束交叠。

## 参数

- `EXPECT_EXTRA=1`：仅在 `-tags=tagExtra` 构建时使用，默认无 Extra；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_methodset_subpkg
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagExtra -o /tmp/case-x . && EXPECT_EXTRA=1 /tmp/case-x
```
