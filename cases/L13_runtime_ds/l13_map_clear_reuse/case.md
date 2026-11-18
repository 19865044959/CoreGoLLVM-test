---
id: l13_map_clear_reuse
dim: L13_runtime_ds
priority: P2
sources:
  - tailscale@v1.80.2: ipn/ipnlocal/cert_test.go:114 测试重置时 `clear(renewCertAt)` 清空证书重试时间表
  - golangci-lint@v2.0.2: pkg/golinters/exptostd/testdata/fix/out/exptostd.go:26 `clear(m)` 取代 x/exp/maps.Clear
tags: [L13, map, clear, reuse, nil-map, nested-map, alias, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 4 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟缓存/重试表清空后复用，`clear` builtin 的语义与生命周期：

1. 3000 项的 `map[string]*item` 执行 `clear`：len 归零、range 零次、旧 key 查找失败，GC 后仍为空；
2. 清空前后写入同 key 的旧/新 value，验证旧指针不会复活；
3. 重填 3000 项、删除每 5 个后再 clear，长度归零且不残留；
4. nil map 与空 map 上重复 `clear` 为安全 no-op，随后可正常写入；
5. `map[int64]struct{}` 清空与重填；
6. 嵌套 map：`clear(outer)` 不销毁仍被别名持有的内层 map，GC 后内层 value 有效；
7. 与逐 key `delete` 循环对照：两者清空后长度一致，重填后查找一致；
8. 每阶段显式 GC + 垃圾分配，最终按排序 key 抽查。

`TORTURE_ROUNDS` 控制外层轮数（默认 4）。

# 期望行为

1. clear 后长度、range、查找全部归零，表可立即复用；
2. 旧 value 指针在清空+重填后不可见；
3. nil/空 map clear 不 panic；
4. 外层 clear 不影响别名持有的内层 map；
5. clear 与 delete 循环行为一致，GC 前后稳定；
6. 最后一行 `ALL PASS l13_map_clear_reuse`。

# 失败判读

- `len after clear` / `range after clear`：clear 未正确重置 map 计数；
- `stale key visible`：clear 后旧桶/key 仍可命中；
- `stale value resurrected`：复用槽位时旧 value 指针未清除；
- `alias lost inner map`：clear 错误地递归清理/释放了共享内层 map；
- panic：对 nil map 调用 clear 或 range 的路径错误；
- GC 后嵌套 value 乱码：map 中指针未被扫描（UAF）。

# 参数

- `UAF_SEED`：value 内容与轮次盐的种子，失败输出带该值；
- `TORTURE_ROUNDS`：轮数，默认 4，范围 1..200。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_clear_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
