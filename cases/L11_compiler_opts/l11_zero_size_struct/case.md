---
id: l11_zero_size_struct
dim: L11_compiler_opts
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/lint/lintersdb/manager_test.go:16 配置 enable/disable 到启用 linter 集合的构建与合并
  - gorm@v1.30.0: tests/associations_many2many_test.go:360 关联集合 map[key]struct{} 并发 Append 去重
  - gorm@v1.30.0: tests/lru_test.go:350 1000 goroutine 并发 LRU Add，最终按 key 去重
tags: [zero-size-struct, struct{}, slice, map-set, channel, interface-equality, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟注册表/集合/信号通道中的零大小类型：`struct{}`、`[16]struct{}`、全零字段结构体与零长数组的 `unsafe.Sizeof`；零大小类型的方法与接口分派；嵌入零大小 struct 的字段与提升方法；`[]struct{}` 的 append/len/cap/range/copy/子切片/元素地址；`map[string]struct{}` 集合的增删查与遍历；零大小 map 键；`chan struct{}` 的缓冲、len/cap、关闭后读取与关闭后发送 panic；`chan *struct{}` 指针跨 GC；零大小动态类型的接口相等性。所有保留容器跨最终 GC 复核。

# 期望行为

1. `struct{}`、`[16]struct{}`、全零字段结构体、`[0]int64`、`[0]string` 的 size 均为 0；
2. 零大小类型的值/指针方法、接口分派与嵌入提升方法结果正确；
3. `[]struct{}` 追加 n 个后 len=n、cap>=n，range 计数为 n，copy 返回 n，子切片长度正确；
4. 取零大小元素地址并解引用安全（不假设地址互不相同）；
5. 零长数组与零长函数数组不进入循环体；
6. `map[string]struct{}` 插 n 个键后 len=n，缺失键 comma-ok 为 false，删除偶数键后 len=n/2、遍历计数一致；
7. 零大小 map 键查询与 comma-ok 正确；
8. `chan struct{}` 发送 n 个后 len=cap=n，GC 后全部取出、len=0；关闭后读取得到零值且 ok=false，再次发送 panic 文本含 `send on closed channel`；
9. `chan *struct{}` 中的指针跨 GC 可解引用；
10. 相同零大小动态类型的接口相等，不同零大小类型不等；
11. 保留的切片/map/嵌入结构体/接口值在最终 GC 后计数与值不变；
12. 最后一行输出 `ALL PASS l11_zero_size_struct`。

# 失败判读

- `size mismatch`：零大小类型布局/填充计算错误；
- `zero-size slice ...`：len/cap/append/copy 路径错误；
- `zero-size value map ...`：零大小 value 的集合去重/删除/遍历错误；
- `zero-size channel ...` / `send-on-closed`：零大小 channel 缓冲计数或关闭语义错误；
- `interface equality`：零大小动态类型比较错误；
- `corrupted after GC`：保留容器或对象被回收/改写；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定集合/切片/channel 的元素个数 `300..399`，失败输出带种子；
- 本用例为 P1，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_zero_size_struct/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_zero_size_struct/*.go
UAF_SEED=1 /tmp/case_n
```
