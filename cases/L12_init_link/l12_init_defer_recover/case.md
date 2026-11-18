---
id: l12_init_defer_recover
dim: L12_init_link
priority: P0
sources:
  - gitea@v1.23.5: tests/e2e/e2e_test.go:33 TestMain 中 :34 defer log.GetManager().Close() 的 LIFO 收尾
  - fiber@v2.52.6: middleware/compress/compress_test.go:17 init() 内 :20 panic(err) 的初始化失败路径
  - tailscale@v1.80.2: cmd/k8s-operator/e2e/main_test.go:56 TestMain 调用 runTests，:59 log.Fatal 收敛错误
tags: [L12, init, defer, LIFO, panic, recover, cross-file-init, gc]
requires: []
runtime: "<3s"
---

# 场景

三个文件各有一个 `init`，跨文件执行顺序按规范未定义。init A 注册三个 defer 并在最后一个中修改包级指针；init B 在嵌套函数中 panic 并由 defer/recover 捕获后继续执行本 init 的后续语句与自身 defer；init C 调用带独立 defer 栈的辅助函数，再 recover 一个非字符串 panic 值。用例只断言每个 init 块内部由规范保证的顺序，不断言跨文件顺序。

## 期望行为

1. 每个 init 的标记块恰出现一次，trace 总长 17；
2. init A 的块为 `a:enter,a:body,a:defer3,a:defer2,a:defer1`（严格 LIFO），defer 中将 `probeRoot.value` 改为 30；
3. init B 的块为 `b:enter,b:panic-pre,b:recover,b:after,b:defer`：recover 后 init 继续并在结束时运行自身 defer；
4. init C 的块为 `c:enter,c:h1,c:h2,c:h3,c:panic-pre,c:recover,c:defer`：辅助函数的 defer 先于外层 panic 恢复，恢复后 init 收尾；
5. `bRecovered==1`、`cRecovered==1`、`cCompleted==true`，进程未终止；
6. 并发只读 trace 与 probe 并穿插显式 GC 时断言全部成立；GC 后 trace、probe 值与恢复状态不变；
7. 最后一行 `ALL PASS l12_init_defer_recover`。

## 失败判读

- 某块缺失或顺序不符：init 内 defer LIFO、panic 恢复后继续执行或同文件 init 语义错误；
- `bRecovered/cRecovered != 1`：recover 未捕获或 init 被重复执行；
- init B/C 后半段标记缺失：recover 后 init 被错误中止；
- GC 后 trace 或 probe 变化：初始化期全局切片/指针未被正确扫描；
- 超时：init 内死锁（本用例无阻塞点，出现即实现异常）。

## 参数

- `UAF_SEED`：决定并发 worker 的起始块轮换与显式 GC 轮数（2..4）；断言与种子无关，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_defer_recover/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
