---
id: l12_init_panic_nested_subprocess
dim: L12_init_link
priority: P1
sources:
  - gorm@v1.30.0: tests/tests_test.go:28 init() 建立测试连接，失败时 :32 os.Exit(1)
  - fiber@v2.52.6: router_test.go:25 init() 读取 fixture，失败时 :28 panic(err)
  - fiber@v2.52.6: middleware/compress/compress_test.go:17 init() 读取文件，:20 panic(err)
  - go@1.24.1: src/runtime/panic.go:724 panic(nil) 的 *runtime.PanicNilError 文本
tags: [L12, init-error, init-panic, panic-nil, deferred-panic, os-Exit, blank-import, nested-package, subprocess, exit-code, gc]
requires: [pkgbuild]
runtime: "<10s（7 个子进程，各带 20s 超时保护）"
---

# 场景

`boom` 子包被 main 空白导入，其 init 按 `L12_INIT_CHILD` 选择失败方式：直接 panic、`panic(nil)`、init 返回时 deferred 函数 panic、deferred `os.Exit(0)`、直接 `os.Exit(7)`、init 内 recover、正常路径。父进程重新执行自身并捕获子进程输出与退出码，验证致命路径下 main 不执行、recover 路径正常进入 main。

## 期望行为

1. `panic`：退出码 2，输出含 `panic: nested init boom`，无 `CHILD-OK`、无 pass 标记；
2. `nil`：退出码 2，输出含 `panic: panic called with nil argument`，main 不执行；
3. `deferpanic`：退出码 2，输出含 `panic: defer boom`（init 正常返回时 deferred panic），main 不执行；
4. `deferexit`：退出码 0，但无 `CHILD-OK`/pass 标记（main 未执行）；
5. `exit7`：退出码 7，无 `CHILD-OK`；
6. `recover`：退出码 0，输出含 `CHILD-OK`，无 `panic:`/`FAIL`；
7. `ok`：退出码 0，输出含 `CHILD-OK`；
8. 所有子进程输出均不含父用例 pass 标记；父进程显式 GC 后 probe 指针与 payload 不变；
9. 最后一行 `ALL PASS l12_init_panic_nested_subprocess`。

## 失败判读

- panic/exit 路径退出码不符：init panic 转换为退出状态或 os.Exit 语义错误；
- panic 路径出现 `CHILD-OK`：main 在 init 失败后仍被执行；
- `nil` 输出文本不符：panicnil GODEBUG 默认或 PanicNilError 文本错误（显式设置 `GODEBUG=panicnil=1` 时本项预期失败）；
- `deferexit` 出现 `CHILD-OK`：init 返回时的 deferred os.Exit 未生效；
- recover 路径无 `CHILD-OK` 或非 0：init 内 recover 后未回到正常初始化流程；
- 子进程超时：init/deferred 调用死锁（20s 上下文超时）。

## 参数

- `UAF_SEED`：决定父进程显式 GC 轮数（2..4），失败信息带出种子；
- `L12_INIT_CHILD`：仅由父进程注入子进程，外部无需设置。

## 运行

```bash
cd cases/L12_init_link/l12_init_panic_nested_subprocess
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
