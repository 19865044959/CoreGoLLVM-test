---
id: l10_dispatch_switch
dim: L10_codegen
priority: P2
sources:
  - sqlc@v1.28.0: internal/codegen/golang/result_test.go:10 TestPutOutColumns_ForZeroColumns 命令类型分派表
  - ent@v0.14.4: schema/field/field_test.go:941 TestTypeString Type 枚举到字符串的分派
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 生成物中的命令/枚举 switch 形态
tags: [L10, L11, dispatch-switch, 1024-opcodes, default-branch, jump-table, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大全量扫描轮次"
---

# 场景

`dispatch_gen.go` 是生成器风格产物：1024 路 `switch op`，每个 case 读取 payload 的 Base 与 `Data[op%4]`，按 `Base*(op%17+1)+b+Data[op%4]+op` 计算。主程序用独立公式核对每个分支、检查 nil/短 payload/越界 opcode 走 default、GC 后保留 payload 的分发结果，并在 `UAF_SEED` 随机数据上做 rounds × 全量 opcode 扫描（`TORTURE_ROUNDS` 放大）。

## 期望行为

1. 固定 payload 下 0–1023 每个 opcode 返回期望值与 true，XOR 校验和一致；
2. 负 Base/Data 的算术结果正确；
3. nil payload、`len(Data)<4`、op >= 1024 全部返回 false；
4. GC 后 16 个保留 payload 的全量扫描仍与公式一致；
5. 随机 rounds（24×`TORTURE_ROUNDS`）× 1024 opcode（随机起点）逐项正确，每位运算累积哈希与期望一致，周期性 GC 后抽查正确；
6. 遍历 0–65535 全部 uint16 opcode，恰有 1024 个返回 true；
7. 最后一行 `ALL PASS l10_dispatch_switch`。

## 失败判读

- 某个 opcode 错：生成 switch 的 case 常量/公式错位（生成器 bug 的典型形态）；
- 越界未走 default：switch 缺少 default 或边界比较错误；
- GC 后结果变化：payload 切片未保活或编译器错误；
- 随机失败可凭 `seed=` 复现，按 round/op 定位；
- `TORTURE_ROUNDS` 下不稳定：随机 payload 复用或哈希计算错。

## 参数

- `UAF_SEED`：随机 payload/起点种子，失败输出带 seed；
- `TORTURE_ROUNDS`：轮次倍数（默认 1，上限 200）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_dispatch_switch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
