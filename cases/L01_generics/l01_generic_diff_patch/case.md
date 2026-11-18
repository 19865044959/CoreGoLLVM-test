---
id: l01_generic_diff_patch
dim: L01_generics
priority: P2
sources:
  - gitea@v1.23.5: services/gitdiff/gitdiff_test.go:40 TestParsePatch_skipTo 按文件名跳过补丁段并统计增删行
  - gitea@v1.23.5: services/gitdiff/gitdiff_test.go:211 TestParsePatch_singlefile 单文件 unified patch 的上下文/增删行解析
  - gitea@v1.23.5: services/repository/files/diff_test.go:18 TestGetDiffPreview 基于基础内容与编辑生成 diff 预览
tags: [L01, generics, diff, patch, LCS, unified-diff, reverse-patch, error-path, gc, UAF_SEED, TORTURE_ROUNDS]
requires: [generics]
runtime: "<30s"
---

# 场景

自建泛型序列 diff/patch：`diffSeq[T comparable]` 用 LCS 动态规划生成 Equal/Delete/Insert 编辑脚本，`applyOps[T]` 严格校验上下文并重建目标序列，`reverseOps[T]` 交换增删得到反向补丁；字符串实例另提供 `formatUnified` 与 `parseUnified`（`@@` hunk 头 + ` `/`-`/`+` 行前缀），模拟 gitea `services/gitdiff` 的 unified patch 解析与上下文匹配。

UAF_SEED 驱动每轮生成 130–152 行字符串序列及随机删/改/插后的 head（首元素必保留，保证至少一个上下文 op），逐项校验 apply/reverse/行覆盖；再格式化补丁、解析后重放，篡改上下文行或删除行必须被拒绝，补丁应用到多一行的 base 也必须报错。另对 []int64 实例做同样校验，并覆盖空↔非空与相同序列边界。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_diff_patch`；
2. `applyOps(base, diffSeq(base, head))` 逐元素等于 head；反向补丁应用到 head 得到 base；
3. 编辑脚本覆盖检查：Equal+Delete 计数等于 len(base)，Equal+Insert 计数等于 len(head)；
4. unified 文本两次格式化结果完全一致，解析出的 op 数与脚本一致且重放等于 head；
5. 篡改任一 Equal 上下文值后 apply 返回错误；篡改 Delete 值同样返回错误；
6. 把补丁应用到多出一行的 base 返回“未完全消费”错误；
7. 缺失 hunk 头或含非法前缀行的补丁文本解析失败；
8. 显式 GC 后 base/head 字符串内容不变；[]int64 实例（含空序列边界）全部校验通过。

# 失败判读

- `apply mismatch` / `reverse apply mismatch`：LCS 回溯方向或反向增删交换错误；
- `delete/insert coverage`：脚本漏行或重复输出，通常是 DP 边界条件错误；
- `parsed op count` / `apply parsed patch`：unified 前缀或行尾处理（空串、`\ No newline`）错误；
- `corrupted ... patch was accepted`：apply 未校验 base 上下文，可能静默产出错误结果；
- `SIGSEGV` / `fatal error: found bad pointer`：字符串切片跨 GC 被破坏（偏 L18）；
- 超时：DP 尺寸被 TORTURE 放大到不合理量级。

# 参数

- `UAF_SEED`：决定序列长度、编辑位置与替换值；失败输出带种子；
- `TORTURE_ROUNDS`：轮数（默认 3）；
- `TORTURE_SIZE`：基础序列长度（默认 130 + seed%23）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_diff_patch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=30 TORTURE_SIZE=800 UAF_SEED=7 /tmp/case
```
