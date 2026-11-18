---
id: l15_tag_const_iota
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_const_iota/const_linux.go:1 linux 的 iota 枚举与派生常量
  - cases/L15_build_tags/l15_tag_const_iota/const_other.go:1 其他平台的 iota 枚举
  - cases/L15_build_tags/l15_tag_const_iota/main.go:23 常量作数组长度
  - cases/L15_build_tags/l15_tag_const_iota/main.go:89 typed enum 作数组下标
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, constant, iota, typed-constant, array-length, constant-expression, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

tag 选择的常量不只是标量：两份 GOOS 互斥文件各自用 `iota` 定义 `Level` 枚举，
再由常量表达式派生 `derivedMask`/`derivedNext`。main 用 `derivedNext` 作数组长度、
用 `Level` 常量作下标与 switch 分支，模拟 protobuf/otel 中按构建标签切换的
枚举与容量常量。

## 期望行为

1. linux：LvLow/LvMid/LvHigh=1/2/3、flavorName=="linux"、flavorBase==10、
   derivedMask==22、derivedNext==4；
2. 其他平台：4/5/6、"other"、20、45、7；
3. `len(levelTable)==derivedNext`，用类型化枚举作下标写入/读取正确，0 号槽保持空；
4. switch 分支与 `int(LvHigh-LvLow)==2` 正确；
5. GC 后常量派生值、数组内容与 payload 不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_const_iota`。

## 失败判读

- 构建失败（数组长度非法/越界）：tag 选择的常量类型或常量表达式错误；
- 枚举值偏移：iota 起点或 typed 常量传播错误；
- derivedMask/derivedNext 不符：跨常量表达式未按选中文件求值；
- 数组内容错位：类型化常量作下标时被错误转换。

## 参数

`BUILD_MODE=module`；无自定义 tag；期望值由 `runtime.GOOS` 推导。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_const_iota
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
