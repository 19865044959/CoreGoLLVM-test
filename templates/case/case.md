---
id: lXX_example_case
dim: LXX_dimension
priority: P1
sources: []
tags: []
requires: []
runtime: "<10s"
---

# 场景

一句话说明这个用例模拟什么真实场景（尽量对应三方库测试里的具体路径）。

## 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS <用例目录名>`；
3. 所有断言（见 main.go 中的 `ck(...)`）成立；
4. 在 clobberfree / efence / k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: ...` 或退出码非 0：期望的指针/数据被破坏，按失败信息定位；
- `SIGSEGV` / `fatal error: found bad pointer`：大概率 stack map 漏标或写屏障缺失；
- 超时：死锁或 GC 饥饿。

## 参数

- `UAF_SEED`：随机路径种子（runner 每次重复注入不同值，失败输出带出该值）；
- 其他 TORTURE_* 参数：<按需填写>。

## 运行

```bash
go build -o /tmp/case ./cases/<dim>/<case>/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
