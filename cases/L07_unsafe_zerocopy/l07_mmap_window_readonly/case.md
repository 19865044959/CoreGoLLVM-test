---
id: l07_mmap_window_readonly
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - sonic@v1.13.2: loader/wrapper_test.go:49 可执行 mmap 机器码与页目录式装载
  - sonic@v1.13.2: loader/loader_go117_test.go:33 自定义模块加载器 mmap/符号注册
  - badger@v4.5.0: y/y_test.go:44 页缓冲跨页写与拼接，页边界必须一致
tags: [L07, unsafe, mmap-style, pages, read-only, liveness]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

模拟内存映射式只读 buffer：在堆上分配 6..8 页（每页 4KiB）的固定大数组，构建页表
`[8]{start unsafe.Pointer; n; sum}`，每页生成 `unsafe.Slice(start, 4096)` 只读窗口，另从
第 0 页尾部派生跨页 96 字节窗口与 64 字节 header string。构建后丢弃大数组变量，页表与
string 视图成为唯一根；每轮显式 GC 后重算页校验和。`window` 辅助函数负责页内/跨页/末字节/
零长边界与越界拒绝。

## 期望行为

1. 页内、跨页、最后一字节、末尾零长窗口有效，负偏移/越界/负长度被拒绝；
2. 页表 start 指针等于对应页起始地址，页起点按 8 字节对齐且相邻页步长恰为 4096；
3. 每页 FNV 校验和等于按模式独立计算的期望值；首/中/末字节抽样一致；
4. header 视图 64 字节与页 0 内容一致；跨页窗口 96 字节与两页拼接内容一致；
5. 丢弃大数组、仅保留页表与 string 视图并显式 GC 后，所有页、header、跨页窗口仍可读；
6. `TORTURE_ROUNDS` 多轮（默认 3，上限 24）的页表全部保留，每轮 GC 后复核此前所有轮次；
7. 全程只用标准库、无真实 mmap 系统调用，输出确定；
8. 末行 `ALL PASS l07_mmap_window_readonly`，失败退出 1。

## 失败判读

- 页校验和不符：页模式/偏移或窗口边界计算错误；
- 页起点不连续/未对齐：页表地址算术错误；
- GC 后崩溃/内容错乱：页表 unsafe.Pointer 槽位未被扫描，或大数组被提前回收；
- 越界窗口未拒绝：边界检查条件错误；
- 仅 stress/k8s 失败：GOGC=1 下多轮保留视图的标记问题。

## 参数

- `UAF_SEED`：决定页数（6..8）与字节模式，失败信息带种子；
- `TORTURE_ROUNDS`：映射轮次放大（默认 3，钳制到 1..24）。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_mmap_window_readonly/*.go
UAF_SEED=1 TORTURE_ROUNDS=6 GODEBUG=clobberfree=1 /tmp/case
```
