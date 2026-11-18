---
id: l12_var_init_panic_subprocess
dim: L12_init_link
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:936 var enumProto2Desc = mustMakeEnumDesc(...) 包级变量初始化器的 must-panic 模式
  - gorm@v1.30.0: tests/tests_test.go:28 init() 连接测试数据库，:32 失败直接 os.Exit(1)
  - fiber@v2.52.6: middleware/compress/compress_test.go:17 init() 读取 fixture，:20 失败 panic(err)
tags: [L12, var-init, panic, recover, subprocess, exit-code, init-order, gc]
requires: []
runtime: "<10s（含 3 个子进程）"
---

# 场景

包级变量初始化器没有隐式 recover 边界。父进程以子进程方式重新执行自身，通过 `L12_VARPANIC_CHILD` 选择三种路径：

- `panic`：`loaded = loadConfig(...)` 在包级变量初始化阶段直接 panic，进程必须在任何 init/main 之前以退出码 2 终止；
- `recover`：初始化器调用的 `safeLoad` 内部 defer/recover 把 panic 转为兜底值，变量初始化继续，init 与 main 正常执行；
- `ok`：正常路径。

父进程检查子进程退出码、panic 文本、init 标记是否存在，并验证自身正常初始化的状态在显式 GC 后不变。

## 期望行为

1. 父进程自身：`loaded.Tag=="loaded"`、`recovered.Tag=="safe"`、init trace 为 `var:before,var:after,init`；
2. panic 子进程退出码为 2，输出含 `panic: l12 var init boom` 与栈中的 `main.loadConfig`；
3. panic 子进程输出不含 `CHILD-INIT-RAN`：init 函数未运行；也不含 pass 标记：main 未运行；
4. recover 子进程退出码 0，输出含 `CHILD-INIT-RAN` 与 pass 标记，且无未恢复 panic 文本；其内部 trace 为 `var:before,var:after,var:recover,init`，recovered 为 fallback/7；
5. ok 子进程退出码 0，输出含 init 与 pass 标记；recovered 为 safe/43；
6. 父进程 8 goroutine 并发读初始化状态 + 显式 GC 时无错误；
7. GC 后 `loaded`/`recovered` 指针身份、trace 与字段不变；
8. 最后一行 `ALL PASS l12_var_init_panic_subprocess`。

## 失败判读

- panic 子进程退出码非 2：变量初始化 panic 未按运行时语义终止进程；
- panic 路径出现 `CHILD-INIT-RAN` 或 pass 标记：变量初始化失败后仍执行了 init/main；
- recover 子进程非 0 或缺标记：初始化器调用函数内的 recover 边界语义错误；
- 子进程超时：初始化路径死锁（30s 子进程超时）；
- 父进程 GC 后状态变化：初始化期全局指针未被正确扫描；
- 失败行 `seed=` 可用于复现父进程 GC 轮数。

## 参数

- `UAF_SEED`：决定父进程显式 GC 轮数（2..4）与并发读取起点；
- `L12_VARPANIC_CHILD`：仅由父进程注入给子进程（`panic`/`recover`/`ok`），外部无需设置。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_var_init_panic_subprocess/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
