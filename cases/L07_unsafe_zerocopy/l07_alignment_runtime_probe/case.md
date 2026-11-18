---
id: l07_alignment_runtime_probe
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - goccy-json@v0.10.5: size_test.go:10 TestOpcodeSize 断言 64 位下结构体大小与字段布局
  - fasthttp@v1.59.0: round2_64_test.go:10 TestRound2ForSliceCap 按对齐/2 的幂向上取整
  - etcd@v3.5.0: pkg/ioutil/pagewriter_test.go:114 checkPageWriter 对非对齐写直接报测试失败
tags: [L07, unsafe, alignment, Alignof, Offsetof, align-up]
requires: []
runtime: "<3s"
---

# 场景

运行时对齐检查：验证基础类型 `Alignof`、混合字段结构体的字段 `Offsetof`/`Sizeof` 对齐
不变量、堆对象字段地址、数组元素地址与步长、slice 数据指针与元素字段地址。再在任意字节
基址上用单表达式 align-up 助手雕刻 uint32/uint64 槽位，断言结果始终对齐且不越界。
最后只保留内部槽位指针与字段指针跨显式 GC。

## 期望行为

1. `Alignof(uint8/16/32/64)==1/2/4/8`，指针/uintptr/string 对齐一致；
2. 结构体对齐等于最大字段对齐，数组对齐等于元素对齐，嵌套结构体偏移按字段对齐倍数；
3. 所有字段 `Offsetof % Alignof(field)==0`，`Sizeof(struct) % Alignof(struct)==0`；
4. 堆对象字段、数组元素、`unsafe.Slice` 元素地址按类型对齐，数组步长等于 Sizeof；
5. slice 数据指针按元素对齐，元素字段（uint64）地址对齐；
6. `alignedPtr` 对任意基址与 0..127 偏移均返回对齐指针，且不越出 256 字节缓冲；
7. 丢弃块/对象/数组变量、只保留槽位指针与字段指针并 GC 后，值、邻居槽位、数组元素仍正确；
8. 末行 `ALL PASS l07_alignment_runtime_probe`，失败退出 1。

## 失败判读

- Alignof/Offsetof 断言失败：类型布局计算错误（32 位目标会触发 64 位 size 断言，按平台判读）；
- 地址对齐断言失败：分配器返回未按类型对齐的内存；
- align-up 结果为非对齐：掩码/进位算法错误；
- GC 后崩溃/efence 报错：内部指针未被当作整对象根；
- 仅 stress/k8s 失败：高 GC 压力下的对象存活问题。

## 参数

- `UAF_SEED`：决定槽位填充值与叶节点值，失败信息带种子；
- 无 TORTURE 参数；结构体大小断言依赖 64 位目标。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_alignment_runtime_probe/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
