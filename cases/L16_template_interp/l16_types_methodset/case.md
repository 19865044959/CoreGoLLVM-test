---
id: l16_types_methodset
dim: L16_template_interp
priority: P1
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 TestEvalMethod：值/指针接收者、嵌入结构体与接口断言
  - yaegi@v0.16.1: interp/interp_eval_test.go:610 通过值与指针调用同一方法集
  - hugo@v0.155.0: codegen/methods_test.go:27 反射/类型方法集枚举
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 接口字段/nil 接收者上的方法调用（bug1）
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:686 接口字段方法集为空的历史 bug（bug2）
tags: [L16, goast, go-types, method-set, embedding, interface, MissingMethod, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

用 `go/types` 对一段含 `Base`（值/指针接收者）、`Mid`（嵌入 Base）、`Outer`（嵌入 *Mid）与三个接口（含接口嵌入）的源码做类型检查，枚举 T 与 *T 的方法集并比较顺序/签名/接收者；再用 `Implements`、`MissingMethod`、`LookupFieldOrMethod` 验证提升、指针接收者与接口实现判定。每轮 GC 后复用缓存的 `*types.Named`/`*types.MethodSet` 再验证一次。

# 期望行为

1. 方法集分别为 Base=[Value]、*Base=[Ptr Value]、Mid=[MidOnly Value]、*Mid=[MidOnly Ptr Value]、Outer/*Outer=[MidOnly OuterOnly Ptr Value]、IOuter=[OuterOnly Value]、*IVal=[]；
2. `Value` 签名为 `func() int`、接收者为 `p.Base`，`Ptr` 接收者为 `*p.Base`；
3. `Implements` 矩阵：Base 不实现 IBoth 而 *Base 实现；Mid 不实现 IBoth 而 *Mid 实现；Outer 实现 IBoth/IOuter；Mid 不实现 IOuter；
4. `MissingMethod(Mid, IBoth, static)` 返回 `Ptr` 且 wrongType=true；Outer 对 IOuter 无缺失；
5. `LookupFieldOrMethod(Outer,"Ptr")` 找到提升方法且 indirect=true、index 长度≥2；`Lookup(Mid,"Ptr")` 可找到但 indirect=false（值不可寻址进方法集）；
6. GC 后所有方法集、选择、接口判定与常量（无）保持不变；
7. 最后一行 `ALL PASS l16_types_methodset`。

# 失败判读

- `method set ... want`：嵌入提升/指针接收者规则实现错误；
- `Value receiver/result`：方法签名或选择对象错误；
- `Implements(...)`：接口实现判定与 Go 1.24 不一致；
- `MissingMethod`：缺失方法/错误类型判定错误；
- `Lookup(...)`：字段/方法提升路径或 indirect 标志错误；
- GC 后查询失败/崩溃：types 对象图被破坏。

# 参数

- `UAF_SEED`：决定轮数（3–5）与 Implements 检查顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_methodset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GODEBUG=efence=1 /tmp/case
```
