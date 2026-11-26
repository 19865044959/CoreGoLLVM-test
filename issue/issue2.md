# BishengGo goret 结果槽未初始化导致 GC 堆完整性崩溃——问题分析报告



## 1. 问题现象



BishengGo 编译的 containerd 2.1.5 执行 overlay 快照层测试时，进程以非确定性概率触发 Go 运行时**堆完整性检查崩溃**，两种形态交替出现（同一缺陷被两个 GC 检查路径命中）：



**形态 A——堆扫描发现坏指针：**



```
fatal error: found bad pointer in Go heap (incorrect use of unsafe or cgo?)
runtime.badPointer(...)          runtime/mbitmap.go:1300
runtime.findObject(...)          runtime/mbitmap.go:1352
```



**形态 B——清扫/标记阶段发现悬空引用：**



```
fatal error: found pointer to free object
runtime.(*mspan).reportZombies(...)   runtime/mgcsweep.go:886
runtime: marked free object in span 0x..., elemsize=160 (bad use of unsafe ...)
runtime: pointer 0x... to unallocated span span.base(...)
```



**现场特征：**



- 崩溃点位于 GC worker 扫描/清扫路径内部，堆元数据本身自洽——不是"被写坏"，而是**存活引用集描述错误**；
- 形态 A 的坏指针固定位于 `overlay.(*snapshotter).Update/Stat`（overlay.go:196/213）**活跃栈帧的 goret 结果槽偏移**（实测 +0xb8 = Info 位图 word3），gccheckmark 取证 `marking free object … found at *(base+off)` 的 base 逐一对应 Update/Stat 帧指针；
- amd64 独有复现（arm64 多轮零复现）；同版本运行时（libstd 7.24.3）的 CoreGo（gc 编译器）预编译产物完全干净——崩溃仅随 gollvm 出现/消失。



## 2. 复现方式



### 2.1 独立复现（推荐，已内置为 LLT 回归用例，100% 确定）



`test/tests/LLT/fixedbugs/issue_goret_slot_dangling.go`（修复提交随附），机制：



- `victim` 与 overlay `Stat/Update` 同构：命名结果 `info` 超过寄存器返回阈值，经 `goret(%info)` **内存槽**（**alloca 位于调用方帧**）返回；结果由经接口调用、**堆逃逸的闭包**晚写入，写前闭包内 `runtime.GC()` 扫栈；
- `dirtyRec` 以深度 d 递归把各层栈帧刷满指向短命堆对象的指针，`runtime.GC()` 清扫成悬挂；`victimRec` 同深度下降（携带相同 byval info 保证帧尺寸一致），使未初始化的 `%goret.actual` alloca 落在脏字上；
- 深度抖动 `d = 3 + i%96` × 30000 轮保证任意栈布局下对齐。

**实测（未修复编译器，单跑 = 一次进程执行）：**

| 版本                       | 结果                                                         |
| -------------------------- | ------------------------------------------------------------ |
| 提交版回归用例（30000 轮） | **20/20 崩溃，全部 ≤7.3s**（unallocated span ×17 + marked free object ×3） |
| 宽轮数变体（120000 轮）    | 14/14 崩溃，≤8.7s                                            |
| 修复后                     | 0/10 全绿（完整跑完打印 PASS，~47s）                         |

### 2.2 containerd 原始载体（历史数据，供追溯）



```
source ~/code/BishengGo/set_env.sh
go test -c -o overlay.test ./plugins/snapshots/overlay
sudo env GOGC=1 ./overlay.test -test.root -test.run='TestOverlay/AsynchronousRemove'
```



约 ~16%/跑触发；`GODEBUG=gcstoptheworld=2` 放大器下 75%（9/12，3-6s）；并行必中版 29-65s 必崩。注意直跑必须带 `-test.root`，否则 RequiresRoot 静默 SKIP（5 字节 PASS 假跑）。



## 3. 排查过程（证据链）



1. **排除环境/构建产物**：同二进制重跑可复现两种形态（18 跑 2 崩）→ 运行时层问题；5 轮冷构建二进制 `.text/.rodata/.symtab` 完全一致（差异仅 DWARF + build-id）→ 与构建非确定性无关。
2. **排除信号抢占**：`GODEBUG=asyncpreemptoff=1` 崩溃率持平（~17%）。
3. **排除并发 GC 竞态**：`GODEBUG=gcstoptheworld=2`（GC 全程 STW，无任何并发窗口）崩溃率反升至 **75%**——竞态假设被推翻，缺陷为**确定性的栈图描述错误**，STW 只是提高扫栈频率。
4. **gccheckmark 取证**：`GODEBUG=gccheckmark=1,gcstoptheworld=2` 3/6 崩且全部 `marking free object … found at *(base+off)`，base 逐一落位 Update 帧 fp（off=+0xb8 恰为 goret 槽内 snapshots.Info 位图 word3）→ 悬空引用持有者 = **goret 结果槽**。
5. **IR 级审计**（`goc -dump-llvm-ir`，overlay/bbolt 全链）：Stat/Update 的 `%goret.formal` **无任何 memset 清零**，却从函数**第一个 statepoint** 起进入 deopt 栈图（Info 指针位图 `{0,1,3,5,8,11}`，gcprog 解码与类型布局精确吻合）；deopt 生成本身逐项正确——缺陷在**槽未初始化**而非位图错误。
6. **对照实验锁定归属**：CoreGo 7.24.3 预编译（gc 编译器，**同一 libstd 运行时版本**）跑完全相同载荷 0/12（容器d 最小用例 0/6 + 放大器 0/6）→ 崩溃仅随 gollvm 出现/消失，根因唯一归属编译器。
7. **定位既有修复的缺口**：cd24507a（10-31，"[bugfix] zero indirect-return goret slot at function entry"，prometheus/etcd 同类崩溃）**已在基线 6a8a8d48 中**，但崩溃仍复现——其清零位于 `CreateFunctionStart` 的 `if (!alloc->heap_)` 分支：命名结果被闭包捕获（**堆逃逸**，即 containerd `WithTransaction` 回调写 Stat/Update 结果的形状）时，该分支被整体跳过，清零从未执行。



## 4. 问题根因



**gollvm 对"命名结果经 goret 内存槽返回 + 结果变量堆逃逸"的形状，缺少入口清零，导致 GC 栈图在结果写入完成前把陈旧栈垃圾描述为活指针。**



缺陷闭环（四个条件叠加）：



1. **槽在调用方帧且未初始化**：超出寄存器返回阈值的命名结果物化为调用方帧中的 `%goret.actual` alloca，作为 `goret(%T)` 参数传给被调者；
2. **栈图无条件描述该槽**：`GoStatePoints.cpp determineDeopt` 从被调者第一个 statepoint 起将 GoRet Argument 记入 deopt bundle，按结果类型 gcprog 标注指针位——不检查槽是否已初始化；
3. **晚写入拉长暴露窗口**：containerd 形状下结果由经 `storage.MetaStore` **接口**调用的闭包写入，闭包因跨接口而**堆逃逸**；窗口 = 函数入口 → 闭包写入完成，含 bbolt 事务毫秒级阻塞；
4. **窗口期槽内容为陈旧栈复用垃圾**：GC 扫栈按位图把垃圾解释为 `snapshots.Info` 的 5 个指针字段 → 扫描侧命中已释放 span（形态 A：bad-pointer / unallocated span），或清扫侧发现"free 但被标"（形态 B：reportZombies / marked free object）。



- `gcstoptheworld=2` 放大 75% 即因 STW 使扫栈更频繁；bbolt 长事务只是拉宽窗口的载体，非缺陷本身；
- CoreGo（gc 编译器）在 `zeroResults`（ssagen/ssa.go）入口即清零结果，故零复现；
- cd24507a 修复了**非逃逸**命名结果（`alloc->heap_ == false` 走 `CreateFunctionStart` 绑定路径）的同型缺陷，但**逃逸**命名结果跳过绑定与清零——本问题正是该缺口。



## 5. 解决方案



`**zeroGoRetArgs**`**（**`**src/CodeGen/LLVM/Passes/GoStatePoints.cpp**`**，+42 行）**：在 statepoint 插入流程中（`insertParsePoints` 内、`zeroAllocInst` 之后调用），对每个结果类型含 manage 指针的 `GoRet` Attribute 参数，在函数入口（跳过头部 alloca 后）插入 memset 清零——



- 不依赖命名结果的逃逸性判定，**无条件**覆盖全部 goret 内存结果槽（补上 cd24507a 的缺口）；
- 与 `zeroAllocInst` 对 address-taken alloca 的既有保护（同文件，注释自述防同类崩溃）同一思路；
- 符合 Go 语义：命名结果入函数即零值；
- 与 CoreGo `zeroResults` 行为对齐。

同时补齐两个 LLT 回归用例（见 §2.1）：`issue_goret_memset_ir.go`（IR 断言入口 memset 存在，确定性拦截编译器回归）+ `issue_goret_slot_dangling.go`（运行时脏栈复现，修复前 20/20 崩、修复后 0 崩）。



### 5.1 与 CoreGo 的逐点对齐



CoreGo（gc 编译器）对同一不变量有明文实现，本修复在语义上与其完全一致：



- **CoreGo 主路径**：`cmd/compile/internal/ssagen/ssa.go:582` 在函数体生成最前调用 `zeroResults()`（:657 定义），注释明示 GC 安全动机——*"For precise stacks, the garbage collector assumes results are always live, so we need to zero them before any allocations"*；
- **CoreGo 逃逸路径**：堆逃逸结果的 heapaddr 槽在 `cmd/compile/internal/liveness/plive.go` 以 `Needzero` 注记入口清零（防 malloc 触发的栈扫撞垃圾）；
- **本修复（gollvm）对应关系**：`zeroGoRetArgs` 在 GoStatePoints pass 的函数入口块插入 memset，实测先于**第一个 statepoint**（该 statepoint 的 deopt 即已含 `%goret.formal`，故顺序是硬要求）；仅覆盖含 manage 指针的 goret 内存槽——非指针槽在 deopt 位图中无指针位、寄存器返回不经内存槽，均无本类 GC 风险；
- **合并覆盖**：cd24507a（非逃逸，CreateFunctionStart 路径）+ 本修复（GoRet 参数无条件路径）合起来与 CoreGo 两条路径（zeroResults + Needzero）一一对应。



## 6. 验证



### 6.1 修复统计（containerd 原始载体）

| 载荷                                     | 修复前                | 修复后                 |
| ---------------------------------------- | --------------------- | ---------------------- |
| `GODEBUG=gcstoptheworld=2` 放大器        | **75%**（9/12，3-6s） | **0/10**               |
| 最小用例（GOGC=1，~16%/跑）              | 10/58                 | **0/10**               |
| 并行必中轰炸（4 worker × 放大器 × 8 轮） | 29-65s 内必崩         | **32 轮全绿**（2993s） |

三层验证：IR 级（`goret.formal` 入口 memset 先于首个 statepoint，且首个 statepoint 的 deopt 同时含该槽——顺序经 `-dump-llvm-ir` 逐行核实：memset@entry 第 2 行 < statepoint@第 4 行）+ 统计矩阵（零假设 P≈1.7×10⁻⁷）+ 高压轰炸；累计 52 轮零崩溃。



### 6.2 独立回归用例（双向）

| 编译器                     | IR 断言               | 运行时用例                                                   |
| -------------------------- | --------------------- | ------------------------------------------------------------ |
| 未修复（6a8a8d48）         | **失败**（无 memset） | **20/20 崩溃**，≤7.3s                                        |
| 修复后（含 zeroGoRetArgs） | 通过                  | **0 崩溃**，3×47s PASS                                       |
| 真实 LLT framework         | —                     | 3/3 PASS（含既有 issue_goret_zero_named_result.go 基线对照） |

无矫枉过正：修复后 IR 中 `gc-relocate` 仍跟踪 `%goret.formal`——写入的合法结果值不会被 GC 误回收，修复只关闭"未初始化窗口"、不影响结果值的正常 GC 活性。



### 6.3 对照组



CoreGo 7.24.3 预编译（gc 编译器，同版本 libstd 运行时）：修复前后均 0 复现（0/12），验证修复不引入行为回归之外的问题，且根因归属自洽。



### 6.4 验证结论（四点核实）



1. **根因正确**：缺陷前提（deopt 从首个 statepoint 无条件描述 goret 槽、修复前槽零初始化）经两轮独立 IR 复核直接可见；崩溃现场偏移与 goret 槽布局逐字对齐；判别实验链闭合。
2. **CoreGo 有对应逻辑**：`zeroResults`（ssagen/ssa.go:582/657，注释明示 GC 精确栈安全动机）+ `Needzero`（liveness/plive.go，堆逃逸结果 heapaddr 槽入口清零）。
3. **逻辑一致**：入口清零、先于首个安全点；cd24507a + 本修复的两条路径与 CoreGo 两条路径一一对应。
4. **能根治**：修复后独立复现 20 崩→0、containerd 三层载荷全归零（P≈1.7×10⁻⁷）、无过回收副作用；amd64 行为级验证完成，`zeroGoRetArgs` 为后端无关 IR pass，arm64 机制等价（建议 arm64 全量 LLT 作最终回归）。



## 7. 影响评估与关联



- **触发面**：任何"大命名结果 + 堆逃逸闭包经接口晚写入"的 Go 程序（containerd overlay、etcd/prometheus 族均有同型历史）；长时间运行、GC 频繁的进程风险更高；
- **历史修复**：cd24507a 覆盖非逃逸路径，本修复覆盖逃逸路径，二者合起来才与 CoreGo `zeroResults` 完全对齐；
- **containerd 侧整改项**：零。