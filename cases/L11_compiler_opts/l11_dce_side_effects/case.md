---
id: l11_dce_side_effects
dim: L11_compiler_opts
priority: P0
sources:
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign.go:10 赋值后未被读取的 dead store
  - golangci-lint@v2.0.2: pkg/golinters/staticcheck/testdata/staticcheck.go:11 自赋值 x = x 与不可达赋值
tags: [dce, side-effect, dead-branch, panic-recover, allocation, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 lint/序列化库中“返回值常被丢弃但调用带副作用”的函数：全局计数自增、对象分配并挂到全局切片、先自增再 panic 的函数。用例故意丢弃返回值、把调用放进结果未使用的循环、使用 `if false`/`if true`/常量比较构造死/活分支，验证死代码消除只能删除纯计算，不能删除全局写、分配与 panic/recover 的可观察效果。100 个对象在 GC 后逐个校验 id 与字节模式。

# 期望行为

1. 纯函数 `pureCalc` 调用可被删除，副作用计数保持 0；
2. 返回值被丢弃的 `bump()`/`addHits(i)` 必须执行，`sideHits` 精确为 1+4950+2=4953；
3. `if false` 分支不得执行，`if true` 分支必须执行，常量比较 `2*3==7` 为假；
4. 结果被使用的纯函数求和为 3725；
5. 100 次丢弃返回值的 `allocBlob` 必须产生 100 次计数与 100 个对象，GC 后 id/字节内容不变；
6. panic 函数返回 `recovered:boom`，panic 计数按调用次数递增（含非 panic 与忽略返回值路径）；
7. 最后一行输出 `ALL PASS l11_dce_side_effects`。

# 失败判读

- `ignored side-effect call was eliminated` / `loop was eliminated`：DCE 越过全局写；
- `allocation side effect lost`：分配被错误删除；
- `dead constant-false branch executed`：分支折叠方向错误；
- `panic side effect lost` / `panic/recover result mismatch`：panic/recover 控制流被破坏；
- `data corrupted after GC`：存活对象被回收或零化。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 不依赖 DCE 一定发生；只要求语义副作用可见。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_dce_side_effects/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_dce_side_effects/*.go && /tmp/case0
```
