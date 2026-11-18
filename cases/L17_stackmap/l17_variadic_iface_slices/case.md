---
id: l17_variadic_iface_slices
dim: L17_stackmap
priority: P0
sources:
  - nats-server@v2.11.0: server/client_test.go:1634（captureWarnLogger.Warnf(format string, v ...any) 变参接口日志路径）
  - fasthttp@v1.59.0: stackless/func_test.go:10（any 参数 + 类型断言调用 4096 次）
tags: [L17, stackmap, variadic, interface, reslice, deferred-closure, GC, UAF_SEED]
requires: []
runtime: "<3s（默认 8 轮）"
---

# 场景

模拟日志/编解码注册的 `...any` 接口，但重点在**变参切片的别名与重切片**：caller 构造 `[]any` 后分别以 `args...`、`args[1:len-1]...` 传给 noinline callee；callee 把收到的切片存入逃逸的 holder，跨两次 GC 后重算 checksum。另以接口变量调用 `foldAll(prefix string, vals ...any)` 变参方法，并在函数末尾用 defer 闭包捕获原始 `[]any` 再做一次 GC 校验。args 内的元素覆盖 `*item`、含指针的 `payload` 值、`[]*item`、字符串与实现 `summer` 接口的 `boxed` 值。

## 期望行为

1. `consume(args[1:len-1]...)` 两次 checksum 相等且等于内层元素期望和；
2. `foldAll` 经接口调度后在变参切片存活期间两次 GC，结果等于 `len(prefix)+vimpl.base+全部期望和`，receiver `keep` 不变；
3. 全部调用后 caller GC，`itA`/`payload`/`[]*item` 三路指针与字符串仍正确；
4. 函数末尾 defer 闭包在 deferreturn 阶段 GC 后仍能完整 fold 原始 args；
5. 最后一行 `ALL PASS l17_variadic_iface_slices`，退出码 0。

## 失败判读

- `fold arg[i]: ... corrupted` / checksum 不符：变参切片头或接口 data 未被栈映射标记，对象被提前回收；
- `foldAll: variadic slice changed across GC`：callee 的变参切片别名槽未被扫描；
- `caller ... corrupted` / `deferred variadic corrupted`：caller 栈上的 `[]any` 头或 defer 闭包捕获的指针失效；
- 仅 clobberfree/efence 下失败：UAF 已发生但默认堆未复用；
- SIGSEGV / `fatal error: found bad pointer`：接口字中的 data 指针被破坏。

## 参数

- `UAF_SEED`：决定每轮 base 与字符串/规模微调，失败输出带种子；
- 无 TORTURE 参数，轮数固定 8，P0 预算内。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_variadic_iface_slices/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
