---
id: l09_cgo_strsep_fields
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 字符串解析边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, strsep, strtok_r, empty-fields, saveptr, in-place, CString, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库两套分词 API 在同一批输入上的语义差异：`strsep` 原位把每个分隔符改写为 NUL 并保留连续/首尾分隔符产生的空 token，`strtok_r` 则跳过空字段且只改写被消费 token 之后的分隔符。用例覆盖连续分隔符、多字符分隔符集合 `";, "`、首尾分隔符、纯分隔符串、空串、分隔符不存在、1000 字节长 token，并在 token 循环中显式 GC。缓冲区由 Go 用 `C.CString` 分配（C 内存），C 不保留 Go 指针。

# 期望行为

1. `"a,,b,,"`：strsep 得到 `["a","","b","",""]`（6 次调用含 NULL），strtok_r 得到 `["a","b"]`（3 次调用）；
2. `"x;y,,z; ;w"` 以 `";, "` 分词时 strsep 保留 7 个字段、strtok_r 得到 4 个非空字段；
3. 分隔符不存在时两者都返回整串（2 次调用）；空串时 strsep 返回 1 个空 token、strtok_r 直接返回 NULL（1 次调用）；
4. 首/尾分隔符、纯分隔符串、`"aa::bb::"` 的空字段模式均符合各自语义；
5. strsep 后缓冲区字节严格等于 `strings.Join(tokens, "\x00") + "\x00"`；
6. strtok_r 后缓冲区中每个 token 之后的分隔符必须已改写为 NUL（未被消费的前导/连续分隔符保持原样），据此重建的 token 序列与返回值一致；
7. 1000 字节长 token 正确；GC 不改变 token 内容或缓冲区；退出码 0，最后一行 `ALL PASS l09_cgo_strsep_fields`。

# 失败判读

- token 序列不符：C 库分词语义差异或缓冲区被越界写；先看失败信息中的 input/delim；
- `strsep raw` 不符：原地改写范围错误（可能误用了 strtok 语义）；
- `strtok_r buffer mutation` 不符：token 后分隔符未被 NUL 改写，或改写越界；
- 调用次数不符：循环提前终止或重复读取；
- 长 token 失败：偏移/长度处理在大缓冲上溢出；
- 崩溃：`C.CString` 缓冲区被越界访问，检查是否误传 Go 字符串指针。

# 参数

- 无随机路径；输入固定，GC 间隔按用例分别为 0/2/3/4 次调用；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_strsep_fields
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
