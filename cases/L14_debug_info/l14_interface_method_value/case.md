---
id: l14_interface_method_value
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_interface_method_value/main.go:38 - 接口实现方法 Handle
  - cases/L14_debug_info/l14_interface_method_value/main.go:49 - 嵌入提升方法 Promoted
  - cases/L14_debug_info/l14_interface_method_value/main.go:155 - 接口方法值/方法表达式符号
  - cases/L14_debug_info/l14_interface_method_value/main.go:172 - 绑定接收者语义
  - docs/DIMENSIONS.md:22 - L14 调试信息（符号/行号）
tags: [L14, method-value, interface, -fm, promoted-method, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

接口方法值 `i.Handle` 在运行期是 `main.imIface.Handle-fm` 包装；接口方法表达式 `imIface.Handle` 不带 `-fm`。嵌入提升的方法值 `pi.Promoted` / `d.Promoted` 分别解析到 `main.imPromIface.Promoted-fm` 与基类方法包装 `main.(*imBase).Promoted-fm`。经接口方法值间接调用并 panic，traceback 必须落到真实实现方法。对应 gorm/echo 等库把接口方法值作为回调注册后崩溃的定位场景。

## 期望行为

1. `i.Handle` 的 funcval 名为 `main.imIface.Handle-fm`，接口方法表达式为 `main.imIface.Handle`，具体方法表达式为 `main.(*imWorker).Handle`；
2. 提升接口方法值为 `main.imPromIface.Promoted-fm`，提升具体方法值为 `main.(*imBase).Promoted-fm`，接口方法表达式为 `main.imPromIface.Promoted`；
3. 接口方法值绑定创建时的动态接收者：之后把接口变量换成另一个 worker，旧方法值仍调用旧接收者（receiver id=1）；
4. traceback 包含 `main.(*imWorker).Handle(`、`main.imInvoke(` 和 `main.go:<行号>`，recover 到原 payload；
5. 提升方法值 traceback 包含 `main.(*imBase).Promoted(` 和 `main.go:<行号>`；
6. 指针接收者方法值观察接收者当前状态（id 修改后调用看到新值）；
7. GC 后 payload 链、嵌入字段与方法值仍有效，最后一行 `ALL PASS l14_interface_method_value`。

## 失败判读

- 接口方法值符号缺少 `-fm` 或接口名：包装函数命名错误；
- 提升方法值解析到 `imDerived` 而非基类方法：提升方法包装归属错误；
- traceback 落到分发层或不含实现方法：接口调用调试信息缺失；
- 绑定接收者 id 不是 1：方法值捕获了后续的接口变量而非创建时接收者。

## 参数

无随机路径。`imInvoke`/`imRun` 均 `//go:noinline`，保证间接调用帧可见；断言依赖精确符号名与相对行号，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_interface_method_value/*.go
GODEBUG=clobberfree=1 /tmp/case
```
