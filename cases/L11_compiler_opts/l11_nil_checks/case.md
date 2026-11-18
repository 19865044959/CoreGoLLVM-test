---
id: l11_nil_checks
dim: L11_compiler_opts
priority: P0
sources:
  - gorm@v1.30.0: schema/field_test.go:92 指针字段 Setter：*string/*uint/*bool/time.Time 与 nil 清零语义
  - echo@v4.13.4: bind_test.go:442 bindData 到 map[string]string 等，nil map 需初始化
tags: [nil-pointer, nil-receiver, typed-nil, nil-map, nil-slice, nil-chan, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 ORM 字段置 nil 与请求绑定中大量 nil 判定：nil 接收者方法（显式 `if c == nil` 分支）、32 个指针元素中混合 nil 与存活对象的循环求和、nil map 读取/删除/迭代与写入 panic、nil slice 的 len/cap/append/copy、typed nil 装入 interface 后 `a != nil` 但断言得到 nil 指针、nil func 调用 panic、nil chan 的 select default 与 close panic、100 个指针元素的循环 nil 计数。关键判定前后穿插垃圾分配与两次 GC，所有 nil 语义必须保持不变。

# 期望行为

1. nil 接收者调用 `incIfLive` 返回 -1、`nameOrNil` 返回 `"nil"`，且指针仍为 nil；
2. 存活计数器自增 5 次后 GC，值仍为 5；
3. 32 元素指针切片的 nil 数与存活元素求和正确，GC 前后一致；
4. nil map：len=0、读取返回零值、不迭代、delete 无 panic、写入 panic；
5. nil slice：len/cap=0、不迭代、append 后长度 3、copy 目标 nil 返回 0；
6. typed nil interface 不等于 nil，类型断言成功但得到 nil 指针；空 interface 等于 nil；
7. nil func 调用 panic；nil chan 的 select 走 default，close(nil) panic；
8. 100 元素循环中 50 个 nil、50 个非 nil 求和 2500，GC 后不变；循环中反复对 nil 接收者做 nil 检查；
9. 最后一行输出 `ALL PASS l11_nil_checks`。

# 失败判读

- `nil receiver ... mismatch`：nil 检查被错误消除导致解引用/返回值错误；
- `typed nil interface ...`：接口 nil 判定与语言规范不符；
- `nil map/slice/chan ...`：nil 容器语义错误；
- `loop nil count` / `sum mismatch after GC`：GC 后指针被错误置 nil 或对象被提前回收；
- `expected panic`：nil map 写入 / nil func 调用 / close(nil) 未 panic。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 不依赖 nil 检查是否被消除，只校验可观察语义。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_nil_checks/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_nil_checks/*.go && /tmp/case0
```
