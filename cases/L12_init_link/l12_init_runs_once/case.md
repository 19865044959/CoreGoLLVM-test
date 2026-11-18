---
id: l12_init_runs_once
dim: L12_init_link
priority: P0
sources:
  - go-redis@v9.8.0: example_test.go:18 init() 构造全局 client 并注册回调
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/mocks_test.go:706 生成代码 init() 注册 descriptor
tags: [L12, init-order, multi-file, runs-once, gc]
requires: []
runtime: "<3s"
---

# 场景

三个文件共 4 个 `init` 函数（一个文件内两个），每次进程启动各执行一次。用例只断言有定义的行为：同文件内 init 按源码顺序执行、所有 init 晚于包级变量初始化、每个 init 恰好一次。跨文件 init 顺序按语言规范未定义，不做断言。

# 期望行为

1. `init_a.go`、`init_b.go`、`init_c.go` 的执行计数各为 1；
2. 全局 trace 恰有 4 个标记，`a#1/a#2/b#1/c#1` 各出现一次；
3. 同文件内的 `a#1` 先于 `a#2`，且第二个 init 能观察到第一个 init 的副作用；
4. 所有 init 都观察到包级变量 `initEpoch=2021`、`initReady=true`，违规计数为 0；
5. GC + 垃圾分配后 trace 长度/内容与 init 计数不变；
6. 最后一行 `ALL PASS l12_init_runs_once`。

# 失败判读

- 某文件计数为 0：init 未执行（链接/初始化任务表丢项）；
- 计数为 2：init 被重复执行；
- `a#1 < a#2` 失败：同文件 init 顺序错误；
- `initBad>0`：init 早于包级变量初始化（初始化阶段顺序错误）；
- GC 后 trace 变化：init 期写入的全局根扫描错误。

# 参数

无随机路径；跨文件 init 顺序未定义，用例不断言顺序，`UAF_SEED` 不参与。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_runs_once/*.go
GODEBUG=clobberfree=1 /tmp/case
```
