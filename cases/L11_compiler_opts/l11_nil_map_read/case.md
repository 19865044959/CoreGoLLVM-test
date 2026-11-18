---
id: l11_nil_map_read
dim: L11_compiler_opts
priority: P0
sources:
  - echo@v4.13.4: bind_test.go:442 bindData 到 map[string]string 等类型时 nil map 需初始化
  - goccy-json@v0.10.5: decode_test.go:2158 TestUnmarshalNulls JSON null 清空预填充的指针/slice/map
  - gorm@v1.30.0: schema/schema_test.go:38 TestParseSchemaWithPointerFields 全指针字段模型的反射递归解引用
tags: [nil-map, map-read, comma-ok, delete, clear, typed-nil, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟请求绑定与反序列化中的 nil map：`len`、缺键读取、空键、int/struct 键、comma-ok、`range`、`delete`、`clear` 与复制全部安全；结构体 nil map 字段读取；函数返回 nil map 后的读取与遍历；嵌套 map 缺键与 nil 内层 map 的链式读取；nil map 指针值；非 nil map 中 nil value 与缺失键的 comma-ok 差异；零值与缺失键区分；typed nil map 装入接口后的非 nil 比较与断言；零大小 value map；大量读取循环。仅写入 nil map 触发 `assignment to entry in nil map` panic，GC 后仍为 nil map。

# 期望行为

1. nil map 的 `len` 为 0，任意键读取返回零值且 comma-ok 为 false；
2. `range` 零次、`delete` 与 `clear` 均为无副作用操作；
3. 复制 nil map 仍是 nil map，读取语义不变；
4. 结构体 nil map 字段读取返回零值，与非 nil 字段互不影响；
5. 函数返回的 nil map 可读取/遍历，非 nil 路径值正确；
6. 嵌套 map 缺键、nil 内层 map 与 `nilNested["a"]["b"]` 链式读取均返回零值；
7. `map[string]*T` 缺键返回 nil 指针；
8. 非 nil map 中显式 nil value 的 comma-ok 为 true，缺失键为 false；
9. `map[int]int64{0:42}` 的键 0 命中，键 1 未命中；
10. typed nil map 装入接口后 `i != nil`，断言成功且得到 nil map，读取/len/comma-ok 正常；
11. 零大小 value map 与 nil 零大小 value map 的 comma-ok 正确；
12. 大量读取循环总和为 0、命中数为 0；
13. GC 后 nil map 与 typed nil 接口保持不变；
14. 写入 nil map panic 文本含 `assignment to entry in nil map`，recover 后仍为空；
15. 最后一行输出 `ALL PASS l11_nil_map_read`。

# 失败判读

- `nil map lookup/len/comma-ok`：nil map 读取返回非零值或错误 ok；
- `nil map range/delete/clear`：零操作路径发生 panic 或改变长度；
- `typed nil map`：接口中的 nil map 被误判为 nil 接口或断言失败；
- `expected panic` / `unexpected panic text`：nil map 写入未 panic 或 panic 来源错误；
- `... after GC`：nil map/接口状态被 GC 破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定读取循环次数 `1500..1999`，失败输出带种子；
- 本用例为 P0，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_nil_map_read/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_nil_map_read/*.go
UAF_SEED=1 /tmp/case_n
```
