---
id: l09_cgo_snprintf_positional
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 侧日志/格式串边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, snprintf, varargs, positional-args, dynamic-width, %n, truncation, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定库的固定参数包装层向 `snprintf` 变参转发时的参数对齐边界：分别覆盖 `%*.*s`、`%*.*lld`（含负宽度、负精度视为省略、精度 0/超过串长）、POSIX 位置参数 `%1$s/%2$lld`、`%1$lld%%%2$s`、`%1$*2$lld`、`%s%n-%hhn`、`%zu`、`%jd`、`%td`、`%hhd`。Go 提供调用期内缓冲区并校验内容、返回值（所需长度）、NUL 位置与零容量/多级截断语义。`%n` 使用 C 侧只读字面量格式（glibc FORTIFY 禁止可写格式串中的 `%n`）。C 只写缓冲区，不保留 Go 指针。

# 期望行为

1. `"[%*.*s]"` w=8/p=3 → `"[     abc]"` 返回 10；w=-6/p=-1 → `"[ab    ]"` 返回 8；w=4/p=0 → `"[    ]"` 返回 6；w=0/p=10 → `"[abc]"` 返回 5；
2. `"[%*.*lld]"` w=10/p=4 → `"[      0042]"` 返回 12；`"%0*.*lld"` w=6/p=-1 对 -42 → `"-00042"`；`"%+*.*lld"` w=8 → `"     +42"`；
3. 位置参数：`"tail/42"`、`"9%z"`、w=6 → `"     7"`、w=-6 → `"7     "`；
4. `"%s%n-%hhn"` 对 `"xy"` 输出 `"xy-"`，`%n` 计数 2、`%hhn` 计数 3；
5. `%zu` 输出 SIZE_MAX=`18446744073709551615`（20 字节），`%jd` 输出 INT64_MAX（19 字节），`%td` 输出 `-12345`，`%hhd` 对 200 输出 `-56`；
6. 容量为 0 时返回所需长度 10 且不写内存；容量 6 时内容为前 5 字节 `"[    "`、NUL 落在 `[5]`；容量 1 时内容为空、NUL 落在 `[0]`；
7. 100 轮不同容量下返回值恒为 10、内容等于完整输出的前缀、NUL 位置正确，GC 不影响结果；
8. 退出码 0，最后一行 `ALL PASS l09_cgo_snprintf_positional`。

# 失败判读

- 数值异常（如 `4294967295`）：包装层参数个数/类型与格式串不匹配，变参寄存器错位（典型：格式未消费 `*` 精度而多传了参数）；
- `%n in writable segment detected`：`%n` 使用了可写格式串，被 glibc FORTIFY 拒绝；应改用只读字面量格式；
- 返回值不符：截断/零容量语义断言错误，或格式串非法导致 snprintf 返回负值；
- NUL 位置不符：缓冲区容量或 NUL 索引计算错误；
- 崩溃：C 写入超过 cap 的 Go 缓冲区，或格式化后继续使用已释放的 CString。

# 参数

- 无随机路径；重复 100 次，容量 1..40 循环，GC 每 25 轮一次；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_snprintf_positional
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
