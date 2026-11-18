---
id: l15_tag_struct_layout
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_tag_struct_layout/layout_linux.go:1 linux 版 eventHeader（3 字段）
  - cases/L15_build_tags/l15_tag_struct_layout/layout_other.go:1 通用版 eventHeader（2 字段）
  - cases/L15_build_tags/l15_tag_struct_layout/main.go:79 unsafe.Sizeof/Alignof 校验
  - cases/L15_build_tags/l15_tag_struct_layout/main.go:83 reflect 字段名与偏移校验
  - cases/L15_build_tags/l15_tag_struct_layout/main.go:109 TORTURE_ROUNDS 分配/读取
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, struct-layout, unsafe, reflection, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

平台特化代码常用构建标签选择不同的结构体布局（例如 epoll/kqueue 事件头）。
linux 版 `eventHeader` 比通用版多一个 `Seq` 字段，`unsafe.Sizeof`、`unsafe.Offsetof`
与 `reflect` 视图都必须随被选中的文件同步切换。TORTURE_ROUNDS 轮在 GC 压力下
反复分配该结构体并读取全部字段。

## 期望行为

1. linux：`platformLayout=="linux"`，字段数 3，`Sizeof==24`，ID/Flags/Seq 偏移为 0/8/16；
2. 非 linux：`platformLayout=="other"`，字段数 2，`Sizeof==16`；
3. `Alignof(eventHeader{})==8`，堆上 32 个对象在 GC 前后字段完整；
4. TORTURE_ROUNDS 轮的 `id+flags+seq` 校验和等于闭式值 `2*R*(R-1)/2 + 2*R*seed`；
5. 最终 GC 后环形对象与 payload 不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_struct_layout`。

## 失败判读

- 文件名选中的布局与 `runtime.GOOS` 不符：GOOS 标签/文件后缀选择错误；
- `Sizeof/NumField` 与平台不符：两个互斥布局文件的标签交集或排除错误；
- 偏移错误但大小正确：字段顺序或结构体布局被破坏；
- 校验和不符：TORTURE 期间对象被覆盖或字段读取错误；
- 失败信息带 `seed=/rounds=`：用同值 `UAF_SEED`、`TORTURE_ROUNDS` 复现。

## 参数

- `TORTURE_ROUNDS`：默认 20000，可放大；
- `UAF_SEED`：参与每轮 flags/seq 计算与校验和，失败时打印；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_struct_layout
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
```
