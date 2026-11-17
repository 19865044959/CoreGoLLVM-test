# CoreGoLLVM GC / 栈映射 UAF 验证工程

针对 `GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md` 中 A–E 类风险构造的可执行用例集：每个用例是一个独立的 `main.go`，自校验、输出确定；编译成二进制后运行，用 **GC 压力 + GODEBUG 调试开关** 把"偶发漏标/漏写屏障"放大成**稳定崩溃或断言失败**。

报告中的 34 个建议场景已覆盖 28 个；未覆盖项见文末（泛型、汇编 ABI0、notinheap 等工具链相关项）。

## 目录结构

```
gc-uaf-tests/
├── run.sh                  # 批量构建 + 多 profile 运行 + 可选 oracle 对照
├── README.md
├── tests/
│   ├── p0_01_stack_ptr_cross_call/
│   │   └── main.go
│   ├── ...                 # 共 21 个用例，每个目录一个 main.go
│   └── p1_21_alloc_shapes/
│       ├── main.go
│       └── .skip-envs      # 该用例跳过的 profile（如 efence/stress）
├── optional/
│   └── generics/main.go    # 泛型用例，默认不跑
└── build/                  # 运行期生成：二进制、日志
```

每个用例都以 `ALL PASS <用例名>` 作为最后一行输出，任一断言失败打印 `FAIL: ...` 并以退出码 1 结束；崩溃/超时由 runner 按退出码捕获。

## 内网快速开始

```bash
cd gc-uaf-tests

# 方式 A：走打了补丁的 go 驱动（报告推荐）
TOOLCHAIN=go ABI=gc ./run.sh

# 方式 B：直接用 llvm-goc
TOOLCHAIN=goc GOC=/path/to/llvm-goc ABI=gc ./run.sh

# 只跑某个用例 / 列出用例
./run.sh p0_09
./run.sh list
```

默认矩阵：`PROFILES=base,clobberfree,efence,stress` × `REPEAT=3`，每个用例最多运行 12 次。全部通过时退出码 0。

### 常用配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `TOOLCHAIN` | `go` | `go`=带 `-go-abi` 的 go 驱动；`goc`=llvm-goc；`stock`=标准 go（本地自检/对照） |
| `GO` / `GOC` | `go` / `llvm-goc` | 对应编译器路径 |
| `ABI` | `gc` | `gc` 或 `gollvm`（旧 ABI，建议配 `TOOLCHAIN=goc`） |
| `BUILD_FLAGS` | 空 | 透传给编译器，如优化级别开关（见下文 -O0） |
| `PROFILES` | `base,clobberfree,efence,stress` | 逗号分隔，可写裸 GODEBUG 项，如 `base,asyncpreemptoff=1` |
| `REPEAT` | `3` | 每个 profile 重复次数 |
| `TIMEOUT` | `120` | 单次运行超时（秒），超时按失败计 |
| `FILTER` | 空 | 用例名子串过滤（也可 `./run.sh <filter>`） |
| `ORACLE` | `0` | `1` 时用标准 go 编译同一份源码并逐行 diff 输出 |
| `KEEP_GOING` | `1` | 失败后是否继续跑后续用例 |

### profile 与环境变量

| profile | 环境 | 暴露的目标 |
|---|---|---|
| `base` | `GOGC=100` | 基本正确性、确定性 |
| `clobberfree` | `GODEBUG=clobberfree=1` | 释放对象填 `0xdeadbeef`，漏标后读到垃圾值 |
| `efence` | `GODEBUG=efence=1` | 对象独占页、释放即 unmap，UAF 立刻 SIGSEGV |
| `stress` | `GOGC=1` + `clobberfree` | 每次分配附近都触发 GC，放大偶发漏标 |

用例内部还会显式调用 `runtime.GC()`、制造垃圾（每轮 64KB）来保证 GC 在关键位置发生。

> 旧版运行时若无 `efence`/`clobberfree` 支持，这两个开关会被忽略，用例仍按 `base` 行为执行；`stress` 的 `GOGC=1` 始终有效。

## 用例清单 ↔ 报告风险点

| 用例 | 覆盖 | 报告条目 |
|---|---|---|
| p0_01_stack_ptr_cross_call | 局部 `*T`/string/slice/map/chan 跨 noinline 调用 + GC | A1/A2（P0-1） |
| p0_02_named_return_defer | 命名返回值被 defer 改写/读取/捕获、panic 后修改 | A1/A2/A11（P0-2） |
| p0_03_addrtaken_array | `var a [N]*T` 取址传参、结构体字段数组、循环内局部数组 | A3（P0-3） |
| p0_04_conditional_big_array | 分支内大指针数组、部分初始化、zero point 调整、循环 lifetime | A3/A2（P0-4、P1-23） |
| p0_05_fca_aggregates | `[2]*T`、`[2][2]*T`、`struct{[3]*T}` 的返回与按值传参 | A4/A5（P0-5、P1-25） |
| p0_06_byval_largeframe | 大结构 byval、大结构返回、9 个 int+8 个指针参数强制 spill | A4/A8/A9（P0-6、P1-21、P1-22） |
| p0_07_derived_interior_ptr | 内部指针/子切片/`&s.arr[i]`/指向字段的指针单独存活 | A6/A7（P0-7、P1-26） |
| p0_08_globals_large | 全局大数组（gcprog）、全局 slice/map/interface/字符串 | B14/B15（P0-8） |
| p0_09_write_barrier_old_young | 老对象→新对象：字段/数组元素/slice 元素/map/接口/chan/全局/append 扩容 | C16（P0-9） |
| p0_10_interface_ptr | 空/非空接口、itab、类型断言/type switch、接口装箱 struct/slice/map | B14/A4（P0-10） |
| p0_11_map_slice_chan | map 扩容/删除、slice append 扩容/copy/子切片、chan 存指针与结构体 | B14/C16（P0-11） |
| p1_12_defer_panic_recover | defer 参数求值、defer 中 recover 后使用指针、循环内 defer 捕获 | A11（P0-12） |
| p1_13_closure_capture | 闭包捕获指针/逃逸地址/切片，经全局变量与接口调用 | A11/A13（P0-13） |
| p1_14_stack_growth | 3000 层递归、8KB 大帧、`//go:nosplit`、递归闭包，运行中 GC | A10（P0-14） |
| p1_15_inline | 内联 vs `//go:noinline` 对照、内联聚合参数、内联分配结果 | A12（P0-15） |
| p1_16_wrappers_methods | 方法值/方法表达式/嵌入 wrapper/值接收者装箱/尾位置调用 | A5/A10（P0-16、P1-27） |
| p1_17_unsafe_uintptr | `unsafe.Pointer`↔`uintptr` 往返、全局/atomic/结构体字段里的 unsafe.Pointer | A13/C16（P0-18） |
| p1_18_finalizer | 可达对象 finalizer 不得执行；失活对象必须执行；finalizer 读到的对象不得损坏 | 漏标探针（P0-20） |
| p1_19_multi_return_variadic | 多返回值各分支、多值命名返回交换、可变参数与转发 | A4/A5（P1-24、P1-28） |
| p1_20_concurrency_asyncpreempt | 4 个 goroutine 并发分配+mutation、紧循环持针，主 goroutine 持续 GC | A13（P2-29、P2-30） |
| p1_21_alloc_shapes | 零大小分配、5 万指针大切片、append 扩容/超容、copy、堆上大结构数组 | D18/D19（P2-33、P2-34） |

## 单个用例手动复现

```bash
# go 驱动
go build -go-abi=gc -o /tmp/uaf ./tests/p0_09_write_barrier_old_young/main.go
GODEBUG=clobberfree=1 /tmp/uaf; echo "exit=$?"

# llvm-goc
llvm-goc -fgo-abi=gc -enable-gc=1 -o /tmp/uaf ./tests/p0_09_write_barrier_old_young/main.go
GODEBUG=efence=1 /tmp/uaf; echo "exit=$?"
```

判读：

| 现象 | 含义 |
|---|---|
| 退出码 139 / `SIGSEGV` / `fatal error: found bad pointer in Go heap` | 栈/全局/堆对象漏标，或写屏障缺失导致对象被回收 |
| 输出里出现 `FAIL:` + 异常值（`clobberfree` 下常见 2305843009213693951 之类的垃圾值） | 确定性的漏标/覆盖 |
| `finalizer ran while object reachable` | 对象仍可达却被判死，典型的 stackmap 漏标 |
| `missing pass marker` 且退出码 0 | 程序提前退出/输出被破坏 |
| 退出码 124 | 超时（死循环或 GC 卡死） |
| 编译失败 | 前端/工具链兼容性问题，先看 `build/<name>.build.log` |

## 判定标准

单个用例通过需同时满足：

1. 退出码 0，且 stdout 最后一行是 `ALL PASS <用例名>`；
2. 在 `clobberfree` 下输出不变；
3. 在 `efence` 下不崩溃（少数大分配用例按 `.skip-envs` 跳过）；
4. `GOGC=1` 压力下重复 N 次稳定（默认 3）；
5. finalizer 探针未提前触发（p1_18）；
6. 可选：`ORACLE=1` 时与标准 go 编译产物输出逐行一致。

## 优化级别与架构

- **-O0/默认两档**：用 `BUILD_FLAGS` 透传。go 驱动可试 `BUILD_FLAGS='-gcflags=all=-N -l'`；llvm-goc 按其支持的优化开关（如 `-O0`）传。`-O0` 更容易暴露 lifetime/zeroing 问题，默认优化更容易暴露 remat/inline 问题，建议两档都跑。
- **双架构**：用例与架构无关，x86_64 与 aarch64 分别跑一遍（栈布局、pcdata 不同）。
- **双 ABI**：`ABI=gollvm TOOLCHAIN=goc` 走旧 statepoint + LSDA stackmap 路径。注意旧 libgo 运行时对 `GODEBUG=efence/clobberfree`、finalizer 的行为可能与 gc 运行时不同，失败时先确认开关是否生效。

## 未覆盖项（需内网按需补充）

| 报告条目 | 原因 | 建议 |
|---|---|---|
| 17 泛型 | 依赖前端对 Go 1.18+ 泛型的支持 | 若支持，把 `optional/generics/main.go` 拷入 `tests/` 运行 |
| 19 汇编函数 ABI0 | `.s` 语法/ABI 与架构、工具链强相关 | 复用 `test/custom/goasm` 的写法构造，验证 `GoAsmFuncStackMap` |
| 32 `//go:notinheap` | 指令支持依赖前端实现 | 用运行时内部类型或照搬现有固定 bug 用例 |
| reflect 指针 | reflect 包在 gccgo 前端下的兼容性未知，易产生非栈映射类假失败 | 单独小规模验证后并入 p1_17 |
| 编译器自身 UAF（E21） | 需要 ASan/valgrind 跑编译器本体 | 用 `-fsanitize=address` 构建 llvm-goc 后重跑本用例集 |

## 与现有测试的关系

本工程是**运行级**验证，和报告 §3.5 的 IR 级测试互补：

- `littest/passes/Statepoints/*`、`littest/passes/StackMaps/*`、`littest/frontend/GC/*` 负责定位 pass 行为；
- 本工程负责"整条链路编译出的二进制是否真的不丢指针"。

如果内网测试框架使用 `test/custom_run.go -go_abi=gc`，可以把 `tests/*/main.go` 分别拷成独立用例（`// run` 指令），但建议保留本 runner 的 profile 矩阵与重复运行能力，这是把偶发 UAF 变成稳定失败的关键。
