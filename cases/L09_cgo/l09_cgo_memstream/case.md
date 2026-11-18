---
id: l09_cgo_memstream
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 库 FILE*/流边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, FILE*, open_memstream, fmemopen, fgets, EOF, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 C stdio 流：`open_memstream` 写入（fwrite/fprintf/fflush/ftell/fseek）与 `fmemopen` 读取（fgets/feof/ferror/fseek）。FILE* 与缓冲全部归 C 管理，Go 只持有不透明结构指针并在调用期内读写；覆盖偏移重写、长度随当前位置更新、EOF/错误状态、空行、跨 GC 继续读写与最终释放。C 不保留任何 Go 指针。

# 期望行为

1. 写入 `hello` + ` world` + `fprintf("%d:%s",7,"cgo")` 后 tell=16，flush 快照为 `hello world7:cgo`，无流错误；
2. seek 0 重写前 5 字节后 tell=5，快照长度 5、内容 `HELLO`（memstream 长度跟随当前位置）；
3. 从偏移 16 继续写 `!` 后快照长度 17、内容 `HELLO world7:cgo!`；close 后返回的 C 缓冲内容一致；
4. 流保持打开时显式 GC，随后写 `gc` + `9:x` 快照长度 5、内容 `gc9:x`，close 后仍一致；
5. fmemopen 读取 `alpha|1\nbeta|2\ngamma|3` 依次得到 8/7/7 字节三行（末行无换行），tell 依次为 8/15/22；
6. 读到 EOF 返回 -1，EOF 标志置位而 error 标志保持 0；
7. seek 0/15/8 后重读对应行正确；
8. 20 轮 seek→读三行→EOF 中周期 GC，内容与标志稳定；
9. 空行文本 `a\n\nb` 的三行分别保留换行；最后一行 `ALL PASS l09_cgo_memstream`。

# 失败判读

- `open_memstream/fmemopen` 失败：glibc/POSIX 2008 支持缺失或长度参数错误；
- `fwrite/fprintf return`：C 包装层参数或流状态错误；
- `tell/snapshot`：未在读取前 flush，或对 open_memstream 长度跟随位置的行为理解错误；
- `buffer after close`：在 fclose 后误以为缓冲失效（fclose 后仍未 free，可直接读）；
- `line/EOF`：fgets 长度、末行无换行或 feof/ferror 混淆；
- `seek` 后内容错误：fseek 未清除 EOF 标志或偏移算错；
- GC 后失败：FILE*/缓冲被 Go GC 误管理（不应发生），或 C 保存了 Go 指针（本用例文本复制进 C 缓冲）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc（`open_memstream`/`fmemopen` 为 POSIX.1-2008）。不访问磁盘文件。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_memstream
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
