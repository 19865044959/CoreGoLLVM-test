---
id: l13_pipeline_var_len
dim: L13_runtime_ds
priority: P1
sources:
  - go-redis@v9.8.0: pipeline_test.go:74 逐级增大到 15 条命令的 pipeline，变长批量写入并校验结果
tags: [L13, pipeline, varint, batch, slice-growth, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s"
---

# 场景

模拟 go-redis pipeline 的变长批量命令：把 1..15 条命令（每条 1..4 个参数）编码为带 magic 的 varint 长度前缀格式，参数长度 0..300B，最大一批追加 32KB 大参数；随后整体解码并逐参数比对。编码走 append 扩容路径，解码为 string 拷贝。GC 插在编码后/解码前、解码结果被改写编码缓冲后，验证编码缓冲与解码 string 的所有权独立。另覆盖空批、坏 magic、超大长度、截断、尾随字节等边界。

# 期望行为

1. 1..15 条命令的每一批都编码成功、解码无错，参数个数/内容逐项一致；
2. 4 的倍数批（n=4,8,12）解码后把整个编码缓冲改写为 `0xAA` 并 GC，解码结果仍与原始命令一致；
3. 每 5 批在解码前显式 GC + 垃圾分配，编码缓冲跨 GC 可用；
4. 每个编码缓冲的所有截断前缀（0、1、2、len/2、len-1）都必须返回错误，不得 panic/越界；
5. 空批返回 0 条命令；坏 magic、`1<<40` 的参数长度、尾随字节均返回错误；合法批正常解码；
6. 单独一轮 32KB 大参数在 GC 后往返一致；
7. 最后一行 `ALL PASS l13_pipeline_var_len`。

# 失败判读

- 解码参数内容错位：varint 长度/偏移推进错误或切片边界计算越界；
- 改写编码缓冲后解码结果变化：解码结果（string）与编码缓冲共享底层数组；
- 截断输入 panic 而非返回错误：解码器缺少剩余长度校验；
- GC 后内容损坏：编码缓冲或解码结果未被正确保活（UAF）；
- 大参数路径失败：大对象分配/存活问题。

# 参数

- `UAF_SEED`：参数长度与内容的种子，失败输出带该种子；
- `TORTURE_ROUNDS`：整批循环次数，默认 1，范围 1..500。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_pipeline_var_len/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
