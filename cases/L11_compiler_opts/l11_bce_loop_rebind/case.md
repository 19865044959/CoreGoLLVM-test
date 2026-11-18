---
id: l11_bce_loop_rebind
dim: L11_compiler_opts
priority: P1
sources:
  - klauspost-compress@v1.17.11: s2/s2_test.go:1528 expand 循环内 copy 后 x=x[idx:] 重切片
  - fasthttp@v1.59.0: client_test.go:2277 copy(p, r.s[r.n:]) 流式读取到调用方缓冲
  - goccy-json@v0.10.5: decode_test.go:3128 TestSliceElemUnmarshaler 切片收缩与底层数组复用
tags: [bounds-check-elimination, loop-hoisting, reslice, slice-header-rebind, panic-recover, escape-analysis, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟压缩解码窗口推进、流式读取和 JSON 切片复用中的 BCE 提升边界：循环体内 slice 头被收缩、旋转、按条件切换到另一切片、通过 `append` 扩容后再索引；另有嵌套窗口循环与“缓存旧长度后再收缩”的过期下标路径。所有合法路径都与独立参考实现或闭式公式对照，越界路径必须 panic。

# 期望行为

1. 逐次收缩的循环和首尾剥离循环的和等于全部元素的固定组合，不因边界检查提升而漏读/多读；
2. 旋转重绑定、交替绑定两个切片、嵌套窗口推进的结果与独立参考实现一致；
3. 循环内 `s = s[:i+1]` 扩展写入的和等于 `3*n*(n-1)/2+n`，底层池按预期写入；
4. 每轮 `append` 扩容后读取末元素的和等于闭式公式；
5. 收缩前缓存旧长度、收缩后再用旧下标访问必须 panic；`s[:cut]` 后访问 `s[cut]`、三索引重切后越过新 cap 也必须 panic；
6. 无操作重绑定（`cut == len`）时最后一元素仍可合法读取；
7. 保留的切片快照在最终显式 GC 后重算所有和仍与期望一致；
8. 默认构建与 `-gcflags='all=-N -l'` 构建全部断言一致；
9. 最后一行输出 `ALL PASS l11_bce_loop_rebind`。

# 失败判读

- `shrinking loop sum` / `peeling loop sum`：循环内重切片的边界被错误提升，读到旧/新长度之外；
- `alternating rebind` / `nested window rebind`：条件重绑定后缓存的 slice 头过期；
- `growing rebind sum/stores`：扩展长度时的边界判断或写入位置错误；
- `stale-length-after-shrink` 未 panic：过期长度上的边界检查被错误消除；
- `retained rebind sums after GC`：保留切片的数据被回收或破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定切片长度 64..94 与数据 salt，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定 3 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_bce_loop_rebind/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_bce_loop_rebind/*.go
UAF_SEED=1 /tmp/case_n
```
