---
id: l08_asm_last_index_sub
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/gitea/tests/integration/issue_test.go:43（LastIndexByte 从 URL 取最后一段）
  - research/repos/gitea/tests/integration/issue_test.go:181（从属性串尾部定位最后一个 '-'）
tags: [L08, asm, amd64, SSE2, LastIndex, backward-scan, substring]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 `bytes.LastIndex` 语义的子串反向定位：从最后一个可能起点 `n-m` 开始，16 字节窗口一次覆盖 16 个候选起点，用 `PCMPEQB` 筛 needle 首字节，`BSRL` 取窗口内最右候选并整段校验；窗口推进到起点 0 后逐起点倒扫。空 needle 返回 n，未命中返回 -1。

# 期望行为

1. nil/空 hay 与空 needle 组合返回 0；空 hay 配非空 needle 返回 -1；needle 长于 hay 返回 -1；
2. 长度 1..257（含 15/16/17/31/32/33/63/64/65）与 needle 长度 1..17 的所有组合，多次出现时返回最后一次出现的下标；
3. 首位/中位/末位命中、只出现一次、完全不出现时与 `bytes.LastIndex` 一致；
4. 尾部候选首字节命中但整段不匹配时继续向前找到更早的真实匹配，不越界读；
5. 大量同首字节（全 'A'）的缓冲在 15/16/17/31/32/33/63/64/65/129 长度下结果正确；
6. 含 0x00 的 hay/needle、子切片、GC + 垃圾分配后结果一致，源数据不变；
7. 最后一行 `ALL PASS l08_asm_last_index_sub`。

# 失败判读

- 返回较早而非最后一次出现：`BSRL` 误用为 `BSFL`，取到窗口内最左候选；
- 返回下标偏 16 的倍数：窗口基址 `R8` 与原始基址的换算错误；
- 窗口切换后漏扫或崩溃：`after_blocks` 分支未把剩余起点正确回退到标量路径；
- 空 needle 返回 -1：入口未单独处理 `m==0`（应为 n）；
- 尾部假候选导致漏报：`BTRL` 清位后未继续检查剩余候选；
- GC 后结果变化：Go 侧未保持 hay/needle 存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_last_index_sub
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
