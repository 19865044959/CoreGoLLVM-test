---
id: l09_cgo_posix_regex
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 库字符串/规则匹配边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, regex, POSIX-ERE, regcomp, captures, glibc, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 libc POSIX 正则：C 辅助函数每次独立 `regcomp/regexec/regfree`，把整体匹配与捕获组范围写入 Go 提供的 `mr_t` 数组。覆盖捕获组、leftmost-longest 整体匹配、锚点、POSIX 字符类、空匹配、重复组、转义、大小写敏感、点号匹配换行、无效模式、无匹配以及显式范围上限；50 轮重复调用中周期 GC。C 不保留任何指针。

# 期望行为

1. `([a-z]+)-([0-9]{2,4})` 在 `"xx abc-123 yy"` 上整体 `[3,10)`、组 `[3,6)`/`[7,10)`，子串为 `abc-123`/`abc`/`123`；
2. `(a|ab)(c|bcd)` 在 `"abcd"` 上整体匹配 `[0,4)`，两个子表达式拼接覆盖整体；
3. `foo|foobar` 在 `"foobar"` 上取 `[0,6)`（leftmost-longest）；
4. `^[0-9]+$` 匹配 `"12345"`、拒绝 `"12a"`；`[[:digit:]]+` 返回 `[2,5)`；alpha/digit 字符类的捕获子串正确；
5. `x*` 在 `"bbb"` 上产生空匹配 `[0,0)`；`z+` 返回无匹配（rc=1）而非编译错误；
6. 非法模式 `"(["` 返回 rc=-1，与无匹配可区分；
7. 转义 `a\+b` 只匹配字面 `a+b`；`[a-z]+` 不匹配 `"ABC"`；未设 `REG_NEWLINE` 时 `^b` 不匹配 `"a\nb"`，`a.b` 匹配 `"a\nb"`；
8. 范围上限 max=2/1 时只写入整体与前 1 个组；
9. 50 轮重复 + 周期 GC 后匹配范围稳定；
10. 最后一行 `ALL PASS l09_cgo_posix_regex`。

# 失败判读

- `basic ...`：范围转换错误（C `int32_t` 与 Go int）或模式/文本指针错误；
- `leftmost-longest ...`：libc 正则方言不是 POSIX ERE（本用例以 glibc 为基准）；
- `subexpressions cover`：捕获组切分未覆盖整体（不同 libc 的子表达式策略差异）；
- `anchored/character class/escaped`：REG_EXTENDED 标志或模式转义错误；
- `empty match/no match/invalid pattern`：返回码约定未区分 0/1/-1；
- `range cap`：C 侧未按 max 截断或越界写；
- GC 后失败：C 保留了 Go 指针（本用例 out 数组只在调用期内写入）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc（POSIX ERE）。匹配偏移按字节计，文本全为 ASCII。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_posix_regex
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
