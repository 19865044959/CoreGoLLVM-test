---
id: l06_iface_vm_stack_dispatch
dim: L06_reflect_iface
priority: P2
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 解释执行中的接口方法集、嵌套嵌入与动态派发
  - yaegi@v0.16.1: interp/interp_eval_test.go:732 解释代码经 reflect 观测宿主值与类型
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:404 非空接口值的方法调用
  - caddy@v2.10.0: replacer_test.go:245 宿主函数 provider 链作为解释器回调
tags: [L06, interface, interpreter, stack-machine, type-switch, embedded-interface, host-callback, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

脚本/模板引擎把指令、宿主能力与运行时值都表示成接口：指令流是 `[]instruction`，指令实现
`exec/opName`，宿主回调经函数表注入。本用例实现一个带变量环境、条件跳转、宿主函数与错误路径
的栈式虚拟机，逐条执行接口指令，期间按步数显式 GC；并把指令流、VM 环境与 tag 指针存入接口槽
后再重算，验证接口动态类型与闭包状态在 GC 后保持不变。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_iface_vm_stack_dispatch`；
2. 算术/条件跳转/宿主回调/`dup` 指令流结果与直接求值一致，`store/load` 环境值正确；
3. type switch 对指令动态类型计数正确；嵌入 `instruction` 的 traced 指令通过提升方法执行；
4. 栈下溢、除零、越界跳转、未知宿主、坏指令、未定义变量、空程序退出深度均返回确定错误文本；
5. 指令流仅由 `[]instruction`/`any` 槽保活时，GC 后重算结果、指令动态类型与 VM 内的 tag 指针
   均不变；
6. 长程序（`TORTURE_WIDTH` 个 push/add）结果与步数精确，默认 40 轮 `TORTURE_ROUNDS` 可放大，
   轮间 GC 后 tag 值正确；
7. 所有 profile 输出一致，无 panic/崩溃。

# 失败判读

- `arithmetic result` / `conditional jump result` / `host callback result`：接口指令派发或栈顺序错误；
- `instruction type switch counts`：指令动态类型/嵌入提升被错误合并；
- `error path ...`：错误分支缺失或错误文本被吞掉；
- `program after GC ...`：接口槽中的指令流、环境 map 或 tag 指针未被扫描；
- `long program ...`：大指令流的 dispatch 或 GC 压力下状态被破坏；
- SIGSEGV：接口数据区按错误元数据扫描。

# 参数

- `UAF_SEED`：变量初值、条件分支与 tag 值来源；失败行带出种子；
- `TORTURE_ROUNDS`：重复解释轮数（默认 40，上限 2000）；
- `TORTURE_WIDTH`：长程序 push/add 对数（默认 64，上限 20000）。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_vm_stack_dispatch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=500 TORTURE_WIDTH=4096 UAF_SEED=7 /tmp/case
```
