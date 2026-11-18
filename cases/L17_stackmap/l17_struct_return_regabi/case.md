---
id: l17_struct_return_regabi
dim: L17_stackmap
priority: P0
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（含指针字段的消息 struct 跨多轮 GC 保留）
  - sonic@v1.13.2: ast/api_native_test.go:132（栈对象装箱成 any，GC/FreeOSMemory 后再读取）
tags: [L17, stackmap, aggregate-return, register-abi, multiple-return, interface, GC, UAF_SEED]
requires: []
runtime: "<3s（默认 12 轮）"
---

# 场景

小聚合走寄存器返回时的栈映射：noinline `makePair` 返回 `(ptr,int)`，`makeStrPtr` 返回 `(string,ptr)`，`pickPair` 有两条返回路径，`splitRet` 返回 `(ptr,string,int)` 三值元组。callee 在返回前两次 GC，返回值同时写入全局锚点；caller 收到后再次 GC，比较返回指针与锚点**是同一对象**，并通过返回指针改写字段后确认锚点同步变化。

另覆盖指针 receiver 与值 receiver 实现 `pairMaker` 接口的 struct 返回（值 receiver 装箱副本中的指针），以及小 struct 装入 `any` 后 GC 再断言。

## 期望行为

1. `makePair` 返回的 `n` 与 `p.v`/`p.next.v` 正确，且 `p` 与全局锚点为同一对象；改写 `p.v` 后锚点可见；
2. `makeStrPtr` 的 string 头与指针在 GC 后完好，`p.name == s`，`p` 与锚点一致；
3. `pickPair(0/1)` 两条路径返回值正确，`splitRet` 的指针/字符串/整数三元组跨 GC 完好；
4. 指针与值 receiver 的接口方法返回 struct 后，返回指针等于 receiver 持有的 `keep`；
5. `any` 装箱的小 struct 跨 GC 断言成功且指针正确；
6. 最后一行 `ALL PASS l17_struct_return_regabi`，退出码 0。

## 失败判读

- `pair pointer corrupted` / `anchor identity broken`：寄存器返回槽或 callee 返回前栈帧中的指针未被标记/重定位；
- `string/pointer return corrupted`：string 头或指针返回槽漏扫；
- `tuple return corrupted`：多返回值中的指针槽位偏移错误；
- `value-receiver maker corrupted`：接口装箱副本中的指针未被扫描；
- 仅 clobberfree/efence 下失败：UAF 已发生但默认堆未复用。

## 参数

- `UAF_SEED`：决定每轮 base，失败输出带种子；
- 无 TORTURE 参数，12 轮固定，P0 预算内。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_struct_return_regabi/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
