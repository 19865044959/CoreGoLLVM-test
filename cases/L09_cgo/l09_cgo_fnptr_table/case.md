---
id: l09_cgo_fnptr_table
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 函数指针分发表）
tags: [L09, cgo, function-pointer, dispatch]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库的算子分发表：C 静态表保存 add/sub/mul/xor/min 五个函数指针，按索引分发；加法/减法/乘法用无符号运算实现以定义回绕语义（避免 C 有符号溢出 UB）。Go 侧逐算子对照并校验非法索引哨兵。

## 期望行为

1. C 表项数为 5 且 5 个函数指针互不相同；
2. 5 个算子 × 9 组参数（含 MinInt64/MaxInt64 与溢出）与 Go 运算一致；
3. 索引 -1/5/6/100/MaxInt32 返回 INT64_MIN 哨兵；
4. 100 轮 × 5 算子跨 GC 重复调用结果稳定；
5. 最后一行 `ALL PASS l09_cgo_fnptr_table`。

## 失败判读

- 分发错误：C 表索引越界检查缺失或函数指针数组初始化错误；
- 溢出结果不一致：C 有符号溢出属 UB，需按无符号运算定义回绕；
- 构建失败：C 表定义放在 preamble 中需为 static（本用例无 //export，允许定义）；
- GC 后错误：纯数值调用，若失败应排查 cgo 调用点代码生成。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_fnptr_table
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
