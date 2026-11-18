---
id: l09_cgo_errno_stdio
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，系统调用/C 库错误边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, errno, stdio, FILE*, ENOENT, EISDIR, ERANGE, strtol, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 libc stdio/strtol 时的 errno 链：每个 C 辅助函数在**同一次调用内**清零 errno、执行操作并立即把 errno 写入 Go 出参，避免跨调用读取线程局部 errno。覆盖缺失文件/空路径 → ENOENT、读目录 → EISDIR、只读流写入与只写流读取的流错误、`w+` 回读、remove/rename 缺失源 → ENOENT，以及 `strtol` 的 LONG_MAX/LONG_MIN 溢出 → ERANGE、部分解析与进制前缀。FILE* 与文件内容归 C/OS 管理，C 不保留 Go 指针；路径由 Go 传入 `C.CString` 且仅调用期内使用。

# 期望行为

1. `fopen` 缺失路径与空路径均返回 NULL 且 errno=ENOENT；
2. `fopen("w")` 成功 errno=0，写入 11 字节返回 11，ftell=11，fclose 返回 0；
3. 重开读取两次 `fgets` 得到 `"alpha\n"`、`"beta\n"`，第三次返回 EOF 且 feof=1、ferror=0；rewind 后可重读，GC 后继续读取正常；
4. 只读流写入返回 0 且 ferror 置位；只写流读取返回 0 且 ferror 置位；
5. 以 `"r"` 打开目录成功，fread 返回 0 且 errno=EISDIR、ferror 置位；
6. `w+` 写入 `"cgo"` 后 rewind 读回 3 字节，再次读取返回 0 且 feof=1；
7. remove/rename 缺失路径返回 -1 且 errno=ENOENT，remove 已存在文件返回 0；
8. `strtol` 正/负溢出分别返回 LONG_MAX/LONG_MIN、errno=ERANGE、endoffset 为完整长度；`"123xyz"`、`"  42xyz"`、`"abc"`、`"0x1f"`（base 0）的返回值与 endoffset 精确匹配；
9. 退出码 0，最后一行 `ALL PASS l09_cgo_errno_stdio`。

# 失败判读

- errno 不符：C 辅助函数未在调用内立即捕获 errno，或 Go 读到了其它线程的 errno；
- `fopen directory succeeds` 失败：平台不允许以文件方式打开目录，属于环境差异；
- 只读/只写流错误未置位：fwrite/fread 的方向检查被绕过；
- `strtol` 返回值不符：LONG_MAX/LONG_MIN 映射或 endoffset 计算错误；
- 文件操作失败导致后续断言连锁：先看 MkdirTemp/WriteFile 是否因权限失败；
- 崩溃：FILE* 已关闭后继续使用或 Go 传入了被 C 保留的 Go 指针。

# 参数

- 使用 `os.MkdirTemp` 创建临时目录（路径不打印，输出确定）；无随机路径；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_errno_stdio
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
