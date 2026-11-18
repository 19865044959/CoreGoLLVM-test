---
id: l06_method_value_iface
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/callbacks_test.go:22 反射方法集探测生命周期回调（值/指针接收者规则）
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 接口包装与动态派发
tags: [L06, method-value, method-expression, interface-dispatch, method-set, GC]
requires: []
runtime: "<10s"
---

# 场景

创建指针接收者 `Inc` 与值接收者 `Get` 的方法值、方法表达式、接口方法值和方法表达式，装入切片/map 后跨 GC 调用；通过匿名嵌入提升方法值并经接口派发；验证值接收者方法值捕获副本但共享指针字段。

# 期望行为

1. 方法值/方法表达式/接口方法值/接口方法表达式的累加结果依次为 `r+5`、`r+8`、`r+12`、`r+13`；
2. `b.Get()` 在 `n=r+10` 时返回 `r+13`（值接收者副本）；
3. map 中 4 个方法值按 key 排序调用，和为 `4r+62`，调用后 `n=r+14`；
4. 匿名嵌入提升的方法值可更新 receiver，提升的 Get 经接口返回 `r+22`；
5. 值接收者方法值在指针字段 V 改为 60 后返回 160，原 receiver 字段再改也不影响快照；
6. 最后一行 `ALL PASS l06_method_value_iface`。

# 失败判读

- `method value after GC` / `interface method value`：闭包捕获的 receiver/接口数据指针跨 GC 失效；
- `value receiver method value snapshot`：值接收者被错误地按引用处理；
- `method value map after GC`：方法值表或 receiver 状态被破坏；
- 崩溃：方法表达式 wrapper 的参数栈映射错误。

# 参数

无随机路径；runner 仍注入 `UAF_SEED` 并仅在失败输出中打印。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_method_value_iface/*.go
GODEBUG=clobberfree=1 /tmp/case
```
