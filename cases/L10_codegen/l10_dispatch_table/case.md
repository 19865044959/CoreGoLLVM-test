---
id: l10_dispatch_table
dim: L10_codegen
priority: P2
sources:
  - sqlc@v1.28.0: internal/codegen/golang/result_test.go:10 TestPutOutColumns_ForZeroColumns 命令类型→输出列的分派表
  - ent@v0.14.4: schema/field/field_test.go:941 TestTypeString Type 枚举到字符串表与越界回退
  - kitex@v0.13.0: pkg/loadbalance/lbcache/hookable_test.go:26 TestHookableRebalance 索引式 hook 表注册/注销
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:365 接口断言/itab 分派热路径
tags: [L10, L11, dispatch-table, func-pointer, closure-capture, holes, indirect-call, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机扫描轮次"
---

# 场景

生成器风格的 dispatch 表：2048 项稠密 `[]func` 表（每 3 项留一个 nil 空洞）加 64 项稀疏 `map[uint32]func` 表，表项在 `init` 中按 opcode 捕获常量。分发优先查稠密表、空洞/越界再查稀疏表，否则 default。用独立公式核对全部 opcode、nil/短载荷、越界与 GC 后行为。

## 期望行为

1. 稠密表长度 2048、活表项 1365，0/3/2046 为空洞、1/2/2047 已注册；稀疏表 64 项，键为 3000+7*i 且无 nil；
2. 固定载荷下 0..5000 全部 opcode 与独立公式一致，成功数恰为 1365+64=1429；
3. 负 Base/极大 Data 算术正确；nil payload、稠密 op 的 `len(Data)<4`、稀疏 op 的 `len(Data)<2`、未注册/空洞/越界 op 全部返回 false；
4. 取出的稠密/稀疏函数值在 GC 后调用仍正确；全量扫描签名与表规模在 GC 后不变；
5. 保留载荷 GC 后内容与分发结果不变；
6. `30×TORTURE_ROUNDS` 轮、每轮 600 个随机 op 与独立公式一致，周期性 GC 后签名与保留载荷复核通过；
7. 最后一行 `ALL PASS l10_dispatch_table`。

## 失败判读

- 所有表项结果偏向最后一个 opcode：闭包按引用捕获循环变量；
- 某 opcode 错位：表项常量/公式生成错误；
- 空洞未走 default：nil 表项未判空；
- 越界未拒绝：缺少范围检查；
- GC 后结果变化：函数值/载荷未保活或编译器问题；
- 随机失败可凭 `seed=` 复现，按 round/op 定位；
- `TORTURE_ROUNDS` 下不稳定：随机载荷/哈希累积错误。

## 参数

- `UAF_SEED`：随机载荷/op 序列种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机轮次倍数（默认 1，上限 200）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_dispatch_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
