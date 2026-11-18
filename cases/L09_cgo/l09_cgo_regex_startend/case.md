---
id: l09_cgo_regex_startend
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 库字符串/规则匹配边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, regex, REG_STARTEND, embedded-NUL, REG_NOTBOL, REG_NOTEOL, backreference, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 libc POSIX 正则时对字节范围匹配的依赖：C 辅助函数每次独立 `regcomp/regexec/regfree`，`regexec` 统一带 `REG_STARTEND`，把 `[start,end)` 字节范围作为匹配窗口，因此可以越过 Go 缓冲区中的内嵌 NUL；C 把整体与捕获组范围写入 Go 提供的 `rx_span_t` 数组。覆盖 NUL 之后的匹配、范围截断、`^`/`$` 与 `REG_NOTBOL`/`REG_NOTEOL`、nmatch 超过组数时的 `-1` 填充、嵌套/可选捕获组、BRE 反向引用、无效模式与越界范围拒绝。正则对象在 C 堆；Go 文本只在调用期内被读取，C 不保留 Go 指针。

# 期望行为

1. `REG_STARTEND` 可用；`"bb"` 在字节串 `"aa\x00bb"` 的 `[0,5)` 范围内匹配到 `[3,5)`，范围 `[0,4)`/`[0,3)` 无匹配，`[3,5)` 匹配；
2. nmatch=8 时，超出捕获组数的 span 全部为 `{-1,-1}`；
3. `^aa` 在完整字符串上匹配 `[0,2)`，`REG_NOTBOL` 或 start=1 时无匹配（`^` 锚定真实字符串起点）；
4. `a+$` 在 `"aabb"` 的 `[0,2)` 范围内匹配 `[0,2)`，`REG_NOTEOL` 或无 end 截断（范围 `[0,4)`）时无匹配；
5. `[0-9]+` 在 `"a12b345c6"` 上依次得到 `[1,3)`、`[4,7)`、`[8,9)`，耗尽后无匹配；
6. `((a+)(b+))` 在 `"aabbb"` 上得到整体/组1/组2/组3 = `[0,5)`/`[0,5)`/`[0,2)`/`[2,5)`；
7. `(a)(b)?` 在 `"a"` 上组2 为 `{-1,-1}`，在 `"ab"` 上组2 为 `[1,2)`；
8. BRE `\(ab\)\1` 在 `"abab"` 上匹配 `[0,4)`，在 `"abac"` 上无匹配；
9. 无效模式 `"("` 与非法反向引用 `\1` 的 `regcomp` 返回非 0；start>end 的调用返回 -2 且不进入 regexec；
10. 同一编译结果重复执行 200 次结果一致，周期性 GC 无影响；退出码 0，最后一行 `ALL PASS l09_cgo_regex_startend`。

# 失败判读

- NUL 之后无匹配：`REG_STARTEND` 未传入或 `pmatch[0]` 范围未设置；
- `^`/`$` 行为不符：libc 对锚点与 `REG_STARTEND` 的解释不同（本用例按 glibc 断言）；
- span 不符：捕获组顺序/leftmost-longest 规则或 `regoff_t` 到 int32 截断错误；
- `unused span != -1`：实现未按 POSIX 填充未使用项；
- 无效模式仍返回对象：`regcomp` 返回值未检查，或 `regfree` 前误用；
- 崩溃：`regex_t` 已释放后复用、Go 文本指针在调用后被 C 保留，或 span 数组容量不足。

# 参数

- 无随机路径；重复执行固定 200 次，GC 每 50 次一次；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_regex_startend
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
