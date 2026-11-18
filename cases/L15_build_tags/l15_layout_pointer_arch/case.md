---
id: l15_layout_pointer_arch
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_layout_pointer_arch/layout_64.go:1 64 位 tag 版 packed（ID 在前）
  - cases/L15_build_tags/l15_layout_pointer_arch/layout_32.go:1 32 位 tag 版 packed（Ref 在前）
  - cases/L15_build_tags/l15_layout_pointer_arch/main.go:92 unsafe.Sizeof/Alignof 校验
  - cases/L15_build_tags/l15_layout_pointer_arch/main.go:96 Offsetof(Ref)/Offsetof(ID) 校验
  - cases/L15_build_tags/l15_layout_pointer_arch/main.go:147 TORTURE_ROUNDS 闭式校验和
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOARCH, struct-layout, unsafe, reflection, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

平台特化代码会用构建标签切换结构体布局。这里 64 位 GOARCH（非 386/arm/mips/mipsle）
选中 `packed{ID int; Ref *blob}`，32 位选中字段顺序相反的 `packed{Ref *blob; ID int}`：
两者大小不同，指针 Ref 的偏移也不同。`unsafe.Sizeof/Alignof/Offsetof`、`reflect`
视图与 GC 指针扫描都必须跟随被选中的布局。TORTURE_ROUNDS 轮在 GC 压力下反复分配、
读取该结构体。

## 期望行为

1. 64 位：`layoutName=="ptr-second"`、`Sizeof==16`、`Ref` 偏移 8、`ID` 偏移 0；
2. 32 位：`layoutName=="ptr-first"`、`Sizeof==8`、`Ref` 偏移 0、`ID` 偏移 4；
3. 两平台 `Alignof==ptrSize`，`reflect.Type` 字段数为 2 且首字段与偏移和布局一致；
4. 64 个堆对象经两次 GC 后，Ref 指向的 blob 字段（含 pad）完整；
5. TORTURE_ROUNDS 轮校验和等于 `3*R*(R-1)/2 + 2*R*seed`；
6. 最终 GC 后环形对象与 payload 不变；
7. 退出码 0，最后一行 `ALL PASS l15_layout_pointer_arch`。

## 失败判读

- `layoutName` 与指针宽度不符：tag 选中的平台文件错误；
- `Sizeof/Alignof` 正确但偏移相反：字段顺序被破坏；
- 偏移正确但 GC 后 blob 损坏：指针扫描使用了另一布局的偏移，属布局/栈映射问题；
- 校验和不符：TORTURE 期间对象被覆盖或读取错误，失败行带 `seed=/rounds=`。

## 参数

- `TORTURE_ROUNDS`：默认 20000，可放大；
- `UAF_SEED`：参与每轮 ID/blob.id，失败信息带出；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_layout_pointer_arch
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
GOARCH=386 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.386 .
```
