---
id: l09_cgo_qsort_cb
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，cgo 边界用真实 C 路径验证）
  - docs/research/tailscale.md:68（cgo/内核边界测试需真实 C 路径）
tags: [L09, cgo, qsort, callback, export, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库排序 API 绑定：Go 把 `[]int32` 传给 C，qsort 在 C 调用期内以函数指针反复回调 `//export goIntCmp`；比较器偶尔分配垃圾并执行 `runtime.GC()`。C 不保留 Go 指针或函数指针到调用结束之后。

## 期望行为

1. 长度 0/1/2/3/7/8/16/33/64/100/257/1000 的数组经 C qsort 后升序且与 Go 排序结果逐元素一致；
2. 300 个重复值/负值端点正确；
3. int32 极值（MinInt32/MaxInt32）排序正确；
4. n<2 时不触发比较器；整体回调次数 >0；
5. 每轮 GC 后结果不变；最后一行 `ALL PASS l09_cgo_qsort_cb`。

## 失败判读

- 构建失败：`//export` 文件 preamble 只允许声明（C 定义移到 qsort.c）；
- 比较器签名为 void* 与 qsort 的 const void* 不兼容：必须通过 C 侧函数指针转换；
- 排序结果错误：比较器返回值符号或 int32 读取错误；
- GC 时崩溃：回调帧栈映射/寄存器扫描问题，或 C 保留了 Go 指针越过调用期。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_qsort_cb
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
