---
id: l15_module_tag_var_chain
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_module_tag_var_chain/conf/conf_linux.go:1 GOOS 选择的常量
  - cases/L15_build_tags/l15_module_tag_var_chain/conf/conf.go:12 变量初始化读取 tag 常量
  - cases/L15_build_tags/l15_module_tag_var_chain/mid/mid.go:8 二级变量依赖
  - cases/L15_build_tags/l15_module_tag_var_chain/out/out.go:9 三级变量依赖
  - cases/L15_build_tags/l15_module_tag_var_chain/main.go:135 TORTURE_ROUNDS 遍历
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, module, multi-package, package-variable, build-tag, GOOS, init-order, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

conf 包的包级变量初始化器读取由 GOOS 文件选择的常量（linux 为 7/"linux"，其他
为 3/"other"），mid → out → main 的变量沿跨包依赖链派生，形成
"tag 常量 → 包变量 → 包变量"的初始化链。TORTURE_ROUNDS 轮中反复遍历由这些变量
构造出的指针切片并在 GC 后复核，模拟对构建标签敏感的业务配置对象图。

## 期望行为

1. linux 下 Base=14、Scale=15、Total=44、mainValue=51、Start=1；其他平台对应 6/7/20/23/0；
2. mid.Table 与 out.Items 的 ID/Label 链与 tag 选择的常量一致；
3. init 顺序恰为 trace → conf → mid → out → main；
4. TORTURE_ROUNDS 轮遍历的 ID 校验和等于按 3 周期计算的闭式值；
5. 最终 GC 后 Items/Node 链与派生值不变；
6. 退出码 0，最后一行 `ALL PASS l15_module_tag_var_chain`。

## 失败判读

- 值来自另一平台分支：GOOS 标签选择的常量文件错误；
- mid/out 值错误而 conf 正确：跨包变量初始化顺序或依赖计算错误；
- init 标记乱序/缺失：包依赖图或 init 顺序错误；
- 失败信息带 `seed=`：用同值 UAF_SEED + TORTURE_ROUNDS 复现。

## 参数

- `TORTURE_ROUNDS`：默认 4000，可放大；
- `UAF_SEED`：决定每轮遍历起点 (r+seed)%3，失败时打印；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_module_tag_var_chain
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
TORTURE_ROUNDS=200000 /tmp/case
```
