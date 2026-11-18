---
id: l17_method_wrapper_tail
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A5/A10 尾调用与 wrapper
tags: [L17, method-value, wrapper, tail-call]
requires: []
runtime: "<3s"
---

# 场景

方法值（捕获 receiver）、方法表达式、多层嵌入产生的 wrapper 链、值接收者装箱进接口、全局函数值，以及尾位置调用。每个调用点都触发 GC，返回后继续使用原指针。

# 期望行为

1. 方法值/方法表达式/嵌入链/接口值接收者调用结果分别为 19/19/19/15；
2. 全局函数值调用为 19；
3. 尾位置调用返回 42，深 wrapper 链返回 19；
4. 最后一行 `ALL PASS l17_method_wrapper_tail`。

# 失败判读

- receiver/tag 指针失效：方法值闭包或 wrapper 参数的栈映射错误；
- 仅 clobberfree 下失败：receiver 对象被提前回收；
- 崩溃：尾调用/wrapper 的帧未按 ABI 正确描述。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_method_wrapper_tail/*.go
GODEBUG=clobberfree=1 /tmp/case
```
