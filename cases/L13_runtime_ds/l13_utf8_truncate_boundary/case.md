---
id: l13_utf8_truncate_boundary
dim: L13_runtime_ds
priority: P1
sources:
  - goccy/go-json@v0.10.5: stream_test.go:529 TestIssue278 长字符串尾部 `\u` 转义贴近缓冲边界
  - sonic@v1.13.2: utf8/utf8_test.go:97 TestValidate_Random 随机无效 UTF-8 字节串校验
  - pgx@v5.7.4: pgtype/hstore_test.go:266 TestRoundTrip 文本编码 key/value 往返（含转义与多字节）
tags: [L13, utf8, truncation, rune-boundary, invalid-sequence, map-key, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟协议字段按字节上限截断且不得切坏 rune：

1. `truncateUTF8(s,max)`：`max<=0` 返回空；`max>=len(s)` 返回原串；否则从 max 回退到 rune 起始字节；
2. 固定合法语料（ASCII、2/3/4 字节 rune、组合音标、韩文、俄文、4KB ASCII、600 个中文）对 0..len+2 每个上限校验：长度上限、前缀、合法 UTF-8、精确 rune 边界、rune 计数、幂等、下一 rune 放不下；
3. 固定非法序列（孤立 continuation、截断 2/3/4 字节、代理区、overlong、超出 U+10FFFF、NUL 混入）对每个上限校验长度、前缀与幂等；
4. `UAF_SEED` 生成 180 个随机合法串（所有字节截断点全检）与 120 个随机非法字节串；
5. 合法串在预算 0/1/3/7/16/64/255/256/1024 下的截断结果作为 map key，GC 后用全新字符串查询并校验 rune 数。

# 期望行为

1. 合法串的截断结果始终最长且不跨 rune，rune 计数与完整 rune 数一致；
2. 截断幂等，非负上限下 `len(out)<=max`；
3. 非法输入只保证前缀/长度/幂等，不 panic、不越界；
4. 截断 key 跨 GC 可查，内容与 rune 数不变；
5. 随机样本全部通过，失败信息带 seed；
6. 最后一行 `ALL PASS l13_utf8_truncate_boundary`。

# 失败判读

- `boundary wrong` / `output invalid UTF-8`：截断回退位置错误；
- `not idempotent`：截断函数对已截断结果不稳定；
- `rune count wrong`：rune 边界或计数实现错误；
- `entry lost after GC` / `fresh lookup miss`：截断字符串所有权错误；
- 超时：逐上限全检规模失控。

# 参数

- `UAF_SEED`：随机语料种子，失败信息附 seed。

# 运行

```bash
go build -o /tmp/l13_utf8_truncate_boundary ./cases/L13_runtime_ds/l13_utf8_truncate_boundary/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_utf8_truncate_boundary
```
