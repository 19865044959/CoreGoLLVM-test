---
id: l03_string_ops_loop
dim: L03_async_preempt
priority: P2
sources:
  - nats-server@v2.11.0: server/sublist_test.go:897 subject 字符串 trie 的匹配长循环，循环中周期性 runtime.GC()
  - tailscale@v1.80.2: net/art/table_test.go:623 同一批 1000 个前缀做 10 次随机打乱插入，1 万次字符串查询与插入顺序无关
tags: [L03, async-preempt, string-scan, strings-builder, substring, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

构造 32KiB 的确定性字符串（26 字节周期重复，并在由种子决定的位置埋入 'Z' 标记）。循环每轮执行 `strings.IndexByte`、`strings.Count("ab")`、随机位置 1024 字节切片，并用 `strings.Builder` + `strconv.Itoa` 生成 token，累计 FNV-1a 哈希。大字符串与最后一轮的切片跨 GC 存活并继续读取。每 16 轮显式 `runtime.GC()`，另有一个 GC pump 并发执行 8 轮 GC。默认 4096 轮，`TORTURE_ROUNDS` 可放大。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_string_ops_loop`；
3. 每轮 IndexByte/Count/切片/marker 位置校验全部通过；
4. Builder token 的长度、前缀/包含关系正确，累计哈希与独立重放结果一致；
5. 最后一轮切片内容与 GC 后重新切片一致，说明子串持续持有底层字符串；
6. 每 16 轮显式 GC 一次，并发 GC pump 至少完成一轮；
7. 最终 `strings.IndexByte(big, 'Z')` 仍返回参考位置；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: string scan/builder checks`：字符串扫描/拼接结果错误，优先查字符串操作相关优化；
- `FAIL: builder token hash stable across GC`：token 内容跨 GC 被破坏或字符串底层数组被回收；
- `FAIL: last slice intact after GC` / `substring keeps backing string alive`：子串指针/长度未正确保活；
- `SIGSEGV`：字符串 header 或底层数组指针在栈上未被正确扫描。

## 参数

- `UAF_SEED`：决定 marker 位置、切片步长与偏移；失败信息带出种子；
- `TORTURE_ROUNDS`：替换默认 4096 轮，用于放大扫描量与 GC 次数。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_string_ops_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=16384 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
