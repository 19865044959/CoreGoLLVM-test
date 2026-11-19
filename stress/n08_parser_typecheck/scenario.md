---
id: n08_parser_typecheck
priority: P1
covers: [L01, L05, L16]
requires: []
default_seconds: 60
max_workers: 8
---

# 场景

前端对象图长跑：每个 worker 每轮用固定模板 + `UAF_SEED` 派生参数在运行时生成约 2000 行合法 Go 源（120 个常量、32 个递归指针 struct、`Pair[T]` 泛型、`Summer` 接口、每类型两方法、32 个接口变量、24 个函数含闭包/defer/type switch），然后：

1. `go/parser.ParseFile`（`ParseComments|AllErrors`），校验错误数 0；
2. `runtime.GC()`（AST 跨 GC）→ `go/types.Check`（`types.Info` 全量映射含 `Instances`），校验包作用域与类型数；
3. `printer.Fprint` 格式化并校验输出；
4. 每 worker 保留 2 份 AST/typeinfo 快照，跨 GC 重新校验 AST 节点计数/checksum、`Info.Types/Uses` 规模、`T000` 方法集。

同一 worker 的生成源逐轮稳定，因此 AST checksum、格式化输出 checksum、类型数、scope 大小必须跨轮完全一致。

## 不变量

1. `ParseFile` 返回 err=nil（parse 错误数 0），AST 节点数 > 4000；
2. `types.Check` 返回 err=nil 且 Error 回调无记录；
3. 包作用域 `TypeName` 数 = 34（32 个 T + Pair + Summer），scope 长度 = 32+2+32+25+120；
4. `Info.Instances/Uses/Types` 非空；
5. 同一 worker 跨轮 `srcLen/astNodes/astSum/fmtLen/fmtSum/scopeLen/typeNames/typeEntries/useEntries/instances` 完全一致；
6. runtime.GC 后保留快照的 AST checksum、typeinfo 规模、scope 与 `T000` 方法集不变；
7. 心跳持续增长，watchdog 无停滞；
8. 结束打印 `STRESS PASS n08_parser_typecheck`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 2，上限 8）
- `STRESS_HEARTBEAT`（默认 5s）
- `STRESS_FIXED_ROUNDS`（默认 6，仅 seconds=0）
- `UAF_SEED`

## 失败判读

- `phase=gen ... generated only N lines`：模板被裁剪，输入规模不足；
- `phase=parse ...`：parser 在合法输入上报错（深递归/AST 分配）；
- `phase=typecheck ...`：类型检查错误、类型数/scope 不符（对象图/泛型实例化问题）；
- `phase=format ...`：printer 输出异常；
- `phase=stability ...`：同一 seed 跨轮 AST/格式化结果漂移（前端非确定性或内存破坏）；
- `phase=retained/fresh/final ... AST changed/type info shrunk/method set lost`：AST/typeinfo 对象图被 GC 漏标或提前回收；
- `watchdog stalled`：前端解析/检查死循环或 O(n²) 病态。

## 运行

```bash
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 FILTER=n08 ./run.sh
SUITE=stress FILTER=n08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 BUILD_FLAGS=-race ./run.sh
```

## 来源

- `go/test/stress/parsego.go:66-75`（生成 Go 源循环 parse 的 stress 形态）
- `go/src/go/parser/performance_test.go:23`（大文件 parser 性能/正确性）
- `go/src/go/types/check_test.go:426-433`（types.Check + Info 全量映射）
