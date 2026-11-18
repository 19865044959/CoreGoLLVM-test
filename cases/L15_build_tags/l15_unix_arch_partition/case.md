---
id: l15_unix_arch_partition
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_unix_arch_partition/part_unix64.go:1 unix && 64 位架构集合
  - cases/L15_build_tags/l15_unix_arch_partition/part_unix32.go:1 unix && 32 位架构集合
  - cases/L15_build_tags/l15_unix_arch_partition/part_other.go:1 三个分区的严格补集
  - cases/L15_build_tags/l15_unix_arch_partition/main.go:78 期望分区推导
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, GOARCH, unix-meta-tag, partition, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

用 `unix` 元标签与 64/32 位 GOARCH 集合把平台划分成 unix64、unix32、windows、
other 四个互斥分区，并像 pion/quic-go 的平台弱口令表那样在 init 中登记分区标记。
main 在 TORTURE_ROUNDS 轮分配中持续使用标签选择的 scale 常数，验证分区唯一性、
常数一致性与 GC 后对象字段。

## 期望行为

1. 当前平台恰好命中一个分区文件，init 标记数量为 1；
2. platformClass/platformScale 与 runtime.GOOS/GOARCH 推导一致：
   linux/amd64 为 unix64/3，windows 为 windows/4，其他未覆盖平台为 other/1；
3. TORTURE_ROUNDS 轮字段和等于闭式公式；
4. 最终 GC 后环形缓冲中所有对象 class/val 仍正确；
5. 退出码 0，最后一行 `ALL PASS l15_unix_arch_partition`。

## 失败判读

- 构建报重复定义：四个分区的并集/补集表达式交叠；
- 分区 init 标记数 != 1：文件选择集合错误；
- ring 节点值错误：标签选择的常数被错误实例化或对象被 GC 提前回收；
- `seed=` 出现在失败信息中：用同值 `UAF_SEED` 复现。

## 参数

- `TORTURE_ROUNDS`：默认 5000，压测可放大到 100000+；
- `UAF_SEED`：决定每轮 val 的偏移（seed%5），失败信息带出；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_unix_arch_partition
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
```
