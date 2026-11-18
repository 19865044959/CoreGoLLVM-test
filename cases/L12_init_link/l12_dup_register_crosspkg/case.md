---
id: l12_dup_register_crosspkg
dim: L12_init_link
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 TestDuplicateCompressorRegister 同名 compressor 重复注册
  - kitex@v0.13.0: pkg/stats/event_test.go:25 DefineNewEvent 重名返回 ErrDuplicated；:36 重名不覆盖旧值
  - ent@v0.14.4: entc/gen/globalid_test.go:72 全局 ID 注册表冲突检测与合并
tags: [L12, module, registry, duplicate-detection, cross-package-init, order-independent, atomicity, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

module 中 `plugin1`、`plugin2` 两个独立包都在 `init` 中向共享 `reg.Default` 注册同名条目 `dup`，但携带不同的值、owner 与指针 payload。二者是根包的兄弟依赖，init 相对顺序未定义。用例不断言谁先注册，只验证与顺序无关的确定结果：恰有一个成功，另一个收到 `ErrDuplicate`，注册表只保留赢家的完整条目。根包随后验证初始化后重复注册不覆盖，以及 16 个 goroutine 并发注册同一新名字时恰有一个赢家。

# 期望行为

1. 两个插件 init 的 outcome 恰有一个为 nil，另一个 `errors.Is(err, reg.ErrDuplicate)`；
2. 注册表长度 1，排序名字为 `[dup]`，条目的 value/owner/payload 与赢家插件一致；
3. 根包初始化后再次注册 `dup` 返回 `ErrDuplicate`，旧值与 payload 不被覆盖；
4. 16 个 goroutine 并发注册 `race`：成功数恰为 1，失败者全部返回 `ErrDuplicate`，条目 value 与成功 goroutine 的槽位一致；
5. 显式 GC 后条目、指针 payload 与重复检测仍有效；
6. 最后一行 `ALL PASS l12_dup_register_crosspkg`。

# 失败判读

- 两个 outcome 都 nil 或都非 nil：注册表原子性/查重缺失；
- 赢家条目与成功 outcome 不匹配：重复注册覆盖了旧值；
- 并发成功数不等于 1：锁内 check-then-set 有竞态；
- GC 后 payload 为 nil 或 tag 错：注册表指针字段未被扫描；
- `names` 非排序结果：排序逻辑错误。

# 参数

- `UAF_SEED`：打乱 16 个并发注册者的槽位映射，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

```bash
cd cases/L12_init_link/l12_dup_register_crosspkg
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
