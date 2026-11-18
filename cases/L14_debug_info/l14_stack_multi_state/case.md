---
id: l14_stack_multi_state
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_stack_multi_state/main.go:59 chan receive 阻塞帧
  - cases/L14_debug_info/l14_stack_multi_state/main.go:68 select 阻塞帧
  - cases/L14_debug_info/l14_stack_multi_state/main.go:80 sleep 状态帧
  - cases/L14_debug_info/l14_stack_multi_state/main.go:87 sync.Mutex.Lock 阻塞帧
  - docs/DIMENSIONS.md:22 L14 调试信息（goroutine 栈 dump）
tags: [L14, runtime.Stack, goroutine-state, chan-receive, select, sleep, mutex, gc]
requires: []
runtime: "<6s"
---

# 场景

同时放置四个处于不同阻塞状态的 goroutine：`chan receive`、`select`、`sleep`、`sync.Mutex.Lock`，用 `runtime.Stack(buf, true)` 做全量 dump。验证多状态 goroutine 的头部状态字符串与函数符号都能正确输出。

## 期望行为

1. dump 中出现 `goroutine <id> [chan receive]:`、`[select]:`、`[sleep]:`、`[sync.Mutex.Lock]:` 四类状态头；
2. dump 包含真实符号 `main.stateChanRecv`、`main.stateSelect`、`main.stateSleep`、`main.stateMutex`、`main.main`；
3. 被唤醒的 chan/select/mutex goroutine 校验各自 payload 指针，GC 后全部有效（errs==0）；
4. 退出码 0，最后一行 `ALL PASS l14_stack_multi_state`。

## 失败判读

- 某状态头缺失：goroutine 状态机或栈展开输出错误（sleep 状态有 5s 轮询兜底，超时仍缺失视为失败）；
- 符号缺失：traceback 符号表问题；
- 唤醒后指针错误：阻塞帧栈映射漏标；
- 超时：状态轮询上限 5s，超过即判失败。

## 参数

无随机路径；sleep goroutine 睡 1 分钟，主流程结束后随进程退出，不等待。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_stack_multi_state/*.go
GODEBUG=clobberfree=1 /tmp/case
```
