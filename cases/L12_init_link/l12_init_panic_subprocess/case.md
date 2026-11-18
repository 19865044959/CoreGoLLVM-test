---
id: l12_init_panic_subprocess
dim: L12_init_link
priority: P1
sources:
  - gorm@v1.30.0: tests/tests_test.go:28 init() 连接测试数据库，失败时 :32 直接 os.Exit(1)
  - fiber@v2.52.6: router_test.go:25 init() 读取 fixture，错误时 panic(err)
  - gitea@v1.23.5: modules/setting/config_env_test.go:78 环境变量配置加载/错误处理语义
tags: [L12, init-error, init-panic, os.Exit, recover, subprocess, exit-code, gc]
requires: []
runtime: "<10s"
---

# 场景

Go 的 `init()` 不能返回 error，真实库通常在 init 中 `panic` 或 `os.Exit` 处理初始化失败。本用例让测试二进制以子进程方式重新执行自身：`L12_INIT_CHILD_MODE=panic` 时 init panic，`exit` 时 init 调用 `os.Exit(7)`，`recover` 时 init 内 `defer/recover` 收敛 panic 后继续 main，`ok` 为正常路径。父进程捕获子进程输出与退出码，并断言 panic/exit 路径下 main 不执行，recover 路径可正常完成。

# 期望行为

1. panic 子进程退出码为 2，输出含 `panic: l12 init boom`，不含 `ALL PASS l12_init_panic_subprocess`；
2. exit 子进程退出码为 7，无 panic 文本，main 不执行；
3. recover 子进程退出码 0，输出含 pass 标记且无 `FAIL:`，说明 init 内 recover 可阻止进程终止；
4. ok 子进程退出码 0 并输出 pass 标记；
5. 父进程 init 只执行一次，显式 GC 轮数随 `UAF_SEED` 变化后仍通过；
6. 父进程最后一行 `ALL PASS l12_init_panic_subprocess`。

# 失败判读

- panic 子进程退出码非 2：panic 未被运行时正确转换为退出状态，或 init 被执行多次/被吞掉；
- panic 路径出现 pass 标记：main 在 init panic 后仍被执行；
- exit 退出码非 7：os.Exit 在 init 中被改写/忽略；
- recover 子进程无 pass 标记或非 0：init 内 recover 语义错误；
- 子进程超时：init 死锁（15s 子进程上下文超时）。

# 参数

- `UAF_SEED`：决定父进程显式 GC 轮数（2..4），失败信息带出种子；
- `L12_INIT_CHILD_MODE`：仅由父进程注入给子进程，外部无需设置。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_panic_subprocess/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
