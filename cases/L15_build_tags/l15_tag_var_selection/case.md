---
id: l15_tag_var_selection
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_tag_var_selection/sel_base.go:1 默认函数变量/注册项/方法
  - cases/L15_build_tags/l15_tag_var_selection/sel_alt.go:1 tagSel 分支
  - cases/L15_build_tags/l15_tag_var_selection/sel_common.go:32 优先级选择与泛型实例化
  - cases/L15_build_tags/l15_tag_var_selection/main.go:108 TORTURE_ROUNDS 一致性循环
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, selection-method, function-variable, init-registry, generics, custom-tags, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

同一功能提供三种 tag 选择方式，模拟 otel-go/consul 中由构建标签切换 exporter 与
后端的做法：包级函数变量 `NewNode`、init 注册的 builder 优先级表、以及泛型函数
在 tag 选择类型上的实例化。默认构建三者都应选中 base；`-tags=tagSel` 时选中 alt。

## 期望行为

1. 默认构建 selVariant=="base"、NewNode 产出的节点 tag=="base"、Scale()==2；
2. registry 含 plain 与选中变体两项，pickHighest 选中优先级 10 的 base；
3. 泛型 buildWith 在 tag 选择类型上返回 `G:kind:base`，在公共类型上返回 `G:const`；
4. `-tags=tagSel` + `EXPECT_SEL=alt` 时以上期望切换为 alt/20/5；
5. TORTURE_ROUNDS 轮经三种机制取数的校验和等于闭式公式；最终 GC 后保留节点、
   优先级选择与泛型实例化结果不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_var_selection`。

## 失败判读

- 三种机制选出不同变体：tag 文件选择与 init 注册/方法集不同步；
- registry 出现两项变体或缺少 plain：init 注册被重复/漏掉；
- 构建报 `Kind`/`Scale` 缺失：tag 文件约束不互补；
- 失败信息带 `seed=`：可用同值 UAF_SEED 与 TORTURE_ROUNDS 复现。

## 参数

- `TORTURE_ROUNDS`：默认 3000，可放大；
- `UAF_SEED`：影响每轮额外偏移（seed%3），失败时打印；
- `EXPECT_SEL=base|alt`：默认 base；alt 仅用于 `-tags=tagSel` 构建；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_var_selection
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOTOOLCHAIN=go1.24.1 go build -tags=tagSel -o /tmp/case-alt . && EXPECT_SEL=alt /tmp/case-alt
TORTURE_ROUNDS=100000 /tmp/case
```
