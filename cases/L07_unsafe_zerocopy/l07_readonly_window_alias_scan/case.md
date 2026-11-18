---
id: l07_readonly_window_alias_scan
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - fasthttp@v1.59.0: header_test.go:325 RawHeaders 与 Peek 规范化视图共享同一缓冲
  - sonic@v1.13.2: ast/node_test.go:137 惰性 raw 子切片在 Load 前不分配
  - badger@v4.5.0: y/y_test.go:44 页缓冲跨页写与拼接，页边界必须一致
tags: [L07, unsafe, read-only-window, subviews, page-boundary, high-cardinality, GC]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

只读窗口的高基数子视图：构造 192..256KiB 的确定性大缓冲（逻辑页 4KiB），在其上派生
96..127 个子视图，同时保留 `unsafe.String` 与 `[]byte` 两种视图。偏移生成覆盖窗口头、
末字节、零长、页首、页尾前 1 字节跨页起点；长度覆盖 0、1、2..32、跨页 2..8 与最长 97。
窗口只读，校验和前后必须一致。丢弃窗口变量后，只由子视图跨显式 GC 保活。模拟 fasthttp
RawHeaders/Peek 共享缓冲、sonic 惰性节点与 badger 页缓冲的只读别名。

## 期望行为

1. 每轮视图数 ≥96，且至少存在跨页视图；
2. 每个视图的 string/[]byte 长度一致，且二者的 data 指针指向窗口同一偏移；
3. 视图 FNV 校验和等于按输入模式独立重算值；首/中/末字节抽样一致；
4. 零长视图为空 string 且校验和为 FNV 初值；
5. 全部视图创建与扫描前后，整窗校验和保持不变（只读语义）；
6. 丢弃窗口变量并显式 GC 后，所有轮次保留的视图仍可读且校验和不变；
7. `TORTURE_ROUNDS` 放大轮次（默认 2，上限 8）不出现悬挂视图；
8. 末行 `ALL PASS l07_readonly_window_alias_scan`，失败退出 1。

## 失败判读

- 视图内容与模式不符：偏移/长度生成或 `unsafe.String` 长度计算错误；
- string 与 []byte 视图不共享地址：发生了隐式复制；
- GC 后崩溃/efence 报错：string/切片头未被当作根，或大对象扫描漏标；
- 只读校验和变化：用例意外写入窗口（用例自身问题）；
- 仅 k8s/stress 失败：GOGC=1 下大对象存活/标记问题。

## 参数

- `UAF_SEED`：决定窗口大小、视图数量与偏移/长度序列，失败信息带种子；
- `TORTURE_ROUNDS`：轮次放大（默认 2，钳制到 1..8），每轮保留一组子视图。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_readonly_window_alias_scan/*.go
UAF_SEED=1 TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
