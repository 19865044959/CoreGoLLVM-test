---
id: l09_cgo_fmemopen_modes
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 库 FILE*/流边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, fmemopen, FILE*, bounded-buffer, append, truncation, EOF, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 C stdio 的定长内存流：缓冲区由 C `malloc` 提供，`fmemopen` 以 `"w"`/`"a"`/`"w+"`/`"r"` 打开，覆盖精确填满 `size-1` 数据 + NUL、`size` 之外字节不受影响、`"w"` 中途 `fseek` 重写后长度/尾部语义、`close` 自动 flush NUL、`"a"` 从首个 NUL 追加、`"w+"` 写入/回绕读取/EOF/`SEEK_CUR`/`SEEK_END`、`"r"` 按行读到 size 边界 EOF、含内嵌 NUL 的定长读取与无 NUL 窗口读取。FILE* 与缓冲区均归 C；Go 只在调用期内读写，C 不保留 Go 指针。

# 期望行为

1. `"w"` size=5 写 `"abcd"`：tell=4，flush 后字节 `"abcd\x00"`，第 5..7 字节仍为哨兵 `'Z'`；
2. `"w"` size=16 写 `"hello world"` 后在偏移 6 重写 `"CGO"`：flush 后 tell=9，前 11 字节为 `"hello CGOld"`，第 11 字节为 0（旧尾部未清除）；
3. `"w"` 写 `"zz"` 后直接 `fclose`：自动 flush，字节为 `"zz\x00"` 且后续哨兵不变；
4. `"a"` 在 `"hi\x00"` 缓冲区上初始 tell=2，追加 `" there"` 后 tell=8，字节为 `"hi there\x00"`，其后哨兵不变；
5. `"w+"` 写 `"xyz"` → flush → rewind 依次读到 x/y/z，再读返回 EOF 且 feof=1；rewind 清除 EOF；`SEEK_CUR -1` 可回退一字节；`SEEK_END` 后 tell=3；ferror=0；
6. `"r"` 在 12 字节 `"line1\nline2\n"` 上两次 fgets 得到两行，第三次返回 EOF（rc=0）、feof=1、ferror=0，rewind 后重新读到第一行；
7. `"r"` 在 5 字节 `"ab\x00cd"` 上 fread 返回 5 字节且字节完全一致（含 NUL），再次读取返回 0 且 feof=1；
8. `"r"` 在 8 字节无 NUL 窗口上 fread 返回 8 字节，再次读取返回 0 且 feof=1；
9. 追加写后的显式 GC 不影响流内容；所有 close 返回 0；退出码 0，最后一行 `ALL PASS l09_cgo_fmemopen_modes`。

# 失败判读

- 精确填满/边界字节不符：size 语义理解错误（可写数据上限为 size-1）或 NUL 写入位置错误；
- 重写后内容不符：`fseek` 位置、flush 时机或旧尾部保留语义与 glibc 不一致；
- `"a"` 初始 tell 不是首个 NUL 偏移：追加模式实现差异；
- `"w+"` 读取 EOF/回退失败：未 flush 或未 `fseek` 就切换读写方向；
- `"r"` 边界 EOF 不符：读取窗口超过 size 或依赖字符串 NUL 截断；
- 崩溃：缓冲区在 close 前被 free，或 `fread` 越界写入 Go 提供的出参缓冲。

# 参数

- 无随机路径；缓冲区内容与偏移固定；仅在 `"a"` 写后插入一次显式 GC；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_fmemopen_modes
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
