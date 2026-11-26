# cel-go库 在 ARM64 上偶现 `fatal error: invalid pointer found on stack`



## 1. 现象



`unit_core` 阶段执行到 `github.com/google/cel-go/interpreter` 包时，进程被运行时致命错误终止，报错首行：



```
runtime: bad pointer in frame github.com/google/cel-go/common/ast.NewCheckedAST at 0x40007fb798: 0xa6
fatal error: invalid pointer found on stack
```



崩溃发生在 GC 后台标记线程收缩/复制某个 goroutine 的栈时：



```
runtime.throw
runtime.adjustpointers
runtime.adjustframe
runtime.copystack
runtime.shrinkstack
runtime.scanstack
runtime.markroot.func1
runtime.markroot
runtime.gcDrain
runtime.gcDrainMarkWorkerDedicated
runtime.gcBgMarkWorker.func2
```



被扫描的 goroutine 当时的调用栈：



```
github.com/google/cel-go/common/ast.NewCheckedAST   common/ast/ast.go:122
github.com/google/cel-go/checker.Check              checker/checker.go:56
github.com/google/cel-go/interpreter.program        interpreter/interpreter_test.go:2503
github.com/google/cel-go/interpreter.TestInterpreter.func1  interpreter/interpreter_test.go:1961
testing.tRunner
```



结果：



```
FAIL	github.com/google/cel-go/interpreter	0.278s
FAIL
```



同一次 `unit_core` 中其余包（`cel`、`common`、`checker`、`parser` 等）均通过，只有 `interpreter` 包失败。



## 2. 复现特征



- **偶现**：约 5 次左右出现 1 次，不是每次必现。
- 触发点是 `interpreter` 包的 `TestInterpreter`：该用例对大量子测试调用 `t.Parallel()`，并发 goroutine 同时执行 `program()` → `checker.Check()` → `ast.NewCheckedAST()`，GC 在此时扫描到 `NewCheckedAST` 的栈帧。
- 崩溃与业务断言无关：没有任何 `--- FAIL` 子测试或断言失败，进程是被运行时直接 abort 的。
- 同一份源码、同一条命令在 x86_64 上连续运行 20 次（含 `GOMAXPROCS=32 -count=5`）未复现，目前只在 ARM64 流水线上观察到。



## 3. 复现命令



### 3.1 流水线原始命令（完整环境）



在 ARM64 构建节点上，按流水线脚本设置环境后执行：



```
export GO111MODULE=on
export GOROOT=<BishengGo>/install
export PATH=<BishengGo>/install/bin:<BishengGo>/install/tools:<BishengGo>/third_party/llvm/linux-aarch64-release/llvm/bin:$PATH
export LD_LIBRARY_PATH=<BishengGo>/third_party/llvm/linux-aarch64-release/bishengllvm/lib:<BishengGo>/install/lib64
export GOMODCACHE=<Quality>/cel-go-0.30.0/pkg/mod
export GOPROXY=off
export GCCGO=<BishengGo>/install/bin/goc
export GCCGOTOOLDIR=<BishengGo>/install/tools

cd <Quality>/cel-go-0.30.0/src/cel-go
go test -p 1 -compiler=gccgo -vet=off -timeout 60m -count=1 ./interpreter/
```



单次运行可能通过，需要重复执行直到命中：



```
for i in $(seq 1 20); do
  echo "=== run $i ==="
  go test -p 1 -compiler=gccgo -vet=off -timeout 60m -count=1 ./interpreter/ || break
done
```



也可以在一次进程内放大 GC/并发压力：



```
GOMAXPROCS=32 go test -p 1 -compiler=gccgo -vet=off -timeout 60m -count=5 -run TestInterpreter ./i
```







## 4. 现场日志



```
runtime: bad pointer in frame github.com/google/cel-go/common/ast.NewCheckedAST at 0x40007fb798: 0xa6
fatal error: invalid pointer found on stack

runtime stack:
runtime.throw({0xffffb8e59dac?, 0xffffb8617404?})
	/CoreGo/src/runtime/panic.go:1160 +0x34 fp=0xffff497ae370 sp=0xffff497ae340 pc=0xffffb8638694
runtime.adjustpointers(0xffff497ae478?, 0x0?, 0x0?, {0x40007fb500?, 0x0?})
	/CoreGo/src/runtime/stack.go:640 +0x1b0 fp=0xffff497ae3b0 sp=0xffff497ae370 pc=0xffffb8616b00
runtime.adjustframe(0xffff497ae478, 0xffff497ae4f8)
	/CoreGo/src/runtime/stack.go:697 +0x1f8 fp=0xffff497ae430 sp=0xffff497ae3b0 pc=0xffffb8616d28
runtime.copystack(0x4000599340, 0x2000)
	/CoreGo/src/runtime/stack.go:935 +0x2cc fp=0xffff497ae550 sp=0xffff497ae430 pc=0xffffb86173fc
runtime.shrinkstack(0x4000599340)
	/CoreGo/src/runtime/stack.go:1237 +0x174 fp=0xffff497ae570 sp=0xffff497ae550 pc=0xffffb86181c4
runtime.scanstack(0x4000599340, 0x4000442450)
	/CoreGo/src/runtime/mgcmark.go:878 +0x20c fp=0xffff497ae6b0 sp=0xffff497ae570 pc=0xffffb85cdacc
runtime.markroot.func1()
	/CoreGo/src/runtime/mgcmark.go:240 +0x8c fp=0xffff497ae700 sp=0xffff497ae6b0 pc=0xffffb85cc2cc
runtime.markroot(0x4000442450, 0x90, 0x1)
	/CoreGo/src/runtime/mgcmark.go:214 +0x24c fp=0xffff497ae7b0 sp=0xffff497ae700 pc=0xffffb85cbfcc
runtime.gcDrain(0x4000442450, 0x3)
	/CoreGo/src/runtime/mgcmark.go:1191 +0x458 fp=0xffff497ae820 sp=0xffff497ae7b0 pc=0xffffb85ce638
runtime.gcDrainMarkWorkerDedicated(...)
	/CoreGo/src/runtime/mgcmark.go:1115
runtime.gcBgMarkWorker.func2()
	/CoreGo/src/runtime/mgc.go:1545 +0xac fp=0xffff497ae870 sp=0xffff497ae820 pc=0xffffb85c963c
runtime.systemstack(0x0)
	/CoreGo/src/runtime/asm_arm64.s:294 +0x6c fp=0xffff497ae880 sp=0xffff497ae870 pc=0xffffb864135c

goroutine 47 gp=0x40005856c0 m=15 mp=0x4000681008 [GC worker (active)]:
runtime.systemstack_switch()
	/CoreGo/src/runtime/asm_arm64.s:251 +0x8 fp=0x4000588710 sp=0x4000588700 pc=0xffffb86412d8
runtime.gcBgMarkWorker(0x4000516070)
	/CoreGo/src/runtime/mgc.go:1527 +0x2c8 fp=0x40005887b0 sp=0x4000588710 pc=0xffffb85c91d8
runtime.gcBgMarkStartWorkers.gowrap1()
	/CoreGo/src/runtime/mgc.go:1372 +0x20 fp=0x40005887d0 sp=0x40005887b0 pc=0xffffb85c8ec0
runtime.goexit({})
	/CoreGo/src/runtime/asm_arm64.s:1273 +0x4 fp=0x40005887d0 sp=0x40005887d0 pc=0xffffb86434b4
created by runtime.gcBgMarkStartWorkers in goroutine 1
	/CoreGo/src/runtime/mgc.go:1372 +0x144
```