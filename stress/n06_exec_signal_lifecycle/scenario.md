---
id: n06_exec_signal_lifecycle
priority: P1
covers: [L02, L04, L19]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

进程/信号生命周期长跑（Unix 优先）。父进程用 `os.Executable()` 自 re-exec，通过环境变量 `STRESS_ROLE` 把同一二进制分派为四种子模式：

- `echo`：打印 `CHILD-ECHO <id>` 后正常退出；
- `hang`：打印 `CHILD-READY` 后长眠，由父进程 `context` cancel 杀死；
- `panic`：打印 `CHILD-BEFORE-PANIC` 后 `panic("n06-child-panic")`；
- `quit`：打印 `CHILD-READY` 后长眠，由父进程发送 `SIGQUIT`，期望 runtime 栈转储后以 exit code 2 退出。

每 worker 每轮：并发 fork/exec 一批 echo 子进程（全局并发上限 10，pipe 读 stdout 校验前缀），再依次做 cancel/panic/SIGQUIT 三个生命周期子进程（同样受该上限约束）。子进程环境剔除 `STRESS_ROLE` 与 `STRESS_CRASH_FILE`（并显式传空），避免覆盖父进程 crash 文件；`GOTRACEBACK` 固定为 `default` 以保证退出码可判读。

## 不变量

1. echo 子进程 exit code 0，stdout 前缀等于 `CHILD-ECHO <id>`；
2. panic 子进程 exit code 2，stdout 为 `CHILD-BEFORE-PANIC`，stderr 以 `panic: n06-child-panic` 开头；
3. SIGQUIT 子进程 exit code 2，stderr 含 runtime 栈转储（`SIGQUIT` 或 `goroutine `）；
4. context cancel 后挂起子进程被杀死（`ctx.Err()==Canceled` 且 `Wait` 返回错误）；
5. 并发 fork/exec 数不超过 10；
6. 结束 `children_started == children_finished`、in-flight 为 0（无未 wait 的僵尸差）；
7. 心跳持续增长，watchdog 无停滞；
8. quiesce 后 goroutine 数不超过启动基线 + 8；
9. 结束打印 `STRESS PASS n06_exec_signal_lifecycle`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 3，上限 8）
- `STRESS_HEARTBEAT`（默认 5s）
- `STRESS_FIXED_ROUNDS`（默认 8，仅 seconds=0）
- `UAF_SEED`

非 Unix（windows）直接打印 `skip: ...` 与 `STRESS PASS` 跳过负载。`open_fds` 只记录不校验。

## 失败判读

- `phase=echo ... stdout=`：子进程输出被截断/串包，或 fork/exec 参数/env 传递错误；
- `phase=panic ... want exit code 2` / `stderr prefix=`：panic 展开或 exit 路径异常（若为 `signal: aborted` 说明 `GOTRACEBACK=crash` 泄漏进子进程）；
- `phase=sigquit ...`：信号未送达、runtime SIGQUIT 转储缺失或退出码不符；
- `phase=cancel ...`：context cancel 未能杀死子进程或 pipe 未关闭；
- `started=... finished=... inflight=`：wait 遗漏（僵尸）或 fork 后未回收；
- `watchdog stalled`：子进程 pipe 读/信号等待死锁。

## 运行

```bash
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 FILTER=n06 ./run.sh
SUITE=stress FILTER=n06 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 BUILD_FLAGS=-race ./run.sh
```

## 来源

- `go/test/stress/runstress.go:84-113`（并发 exec/pipe 模式）
- `go/src/os/exec/exec_test.go:616-657,1708-1776`（context cancel/pipe/ExtraFiles）
- `go/src/runtime/crash_test.go:37-49,81-133`（自 re-exec + crash 文本判读）
- `go/src/runtime/crash_unix_test.go:57-148`（SIGQUIT/信号生命周期）
