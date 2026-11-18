---
id: l11_switch_opt
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: router_test.go:951 6 条 static/param/any 混合路由的多级回溯
  - echo@v4.13.4: router_test.go:1368 插入 /te/st* 触发节点拆分，RouteNotFound handler 必须保留
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 TestDumpOpcode 用 type switch 分派 opcode
tags: [switch, jump-table, if-chain, string-switch, dispatch, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 256 轮 × 4160 个输入；TORTURE_ROUNDS=100 时 25600 轮）"
---

# 场景

模拟路由树/opcode 分派中的大型分支结构：一个 320 分支的整数 `switch`（返回值由多项式公式生成，默认分支 -1）、一个 64 段 if/else 链（每段覆盖 4 个整数，含负值与越界默认值）、一个 64 分支字符串 `switch`（`"k000".."k063"`）。入口期望值由不复用 switch/if 链的参考函数独立推导。每轮按 `UAF_SEED` 旋转遍历 `-64..4095`，逐项比较，每 32 轮插入垃圾分配与两次 GC；`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. 320 分支 switch 对全部输入与 `refSwitch` 一致，默认值 -1；
2. 64 段 if/else 链对全部输入与 `refIf` 一致，`x>=256` 返回 -2、`x<0` 返回 -1；
3. 64 分支字符串 switch 对 `k000..k063` 返回索引，未知字符串返回 -1；
4. 边界值 0/3/4/255/256/319/320/-1 与参考函数一致；
5. 遍历起点由种子决定且覆盖全部输入，GC 后边界检查结果不变；
6. 默认 256 轮，实际轮数为 `256 * min(TORTURE_ROUNDS,100)`；
7. 最后一行输出 `ALL PASS l11_switch_opt`。

# 失败判读

- `large switch mismatch`：跳转表/二分查找分派错误（记录首个失配输入）；
- `if-chain mismatch`：分支比较顺序或边界常量错误；
- `string switch mismatch`：字符串哈希/比较分派错误；
- `boundary ... mismatch` / `default mismatch`：边界或默认分支处理错误；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮遍历起点旋转量，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（即 256 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_switch_opt/*.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=50 UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_switch_opt/*.go && UAF_SEED=7 /tmp/case0
```
