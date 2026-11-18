---
id: l07_byte_to_string_lifetime
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 CopyString 开关决定 string 是否引用输入 []byte
  - gin@v1.10.0: internal/bytesconv/bytesconv_test.go:28 []byte→string 零拷贝别名与复制版对照
  - sonic@v1.13.2: decoder/decoder_native_test.go:276 修改输入 buffer，观察别名/拷贝模式输出差异
tags: [L07, unsafe, string, lifetime, alias-vs-copy]
requires: []
runtime: "<10s"
---

# 场景

`unsafe.String(unsafe.SliceData(b), len(b))` 生成的 string 与 `b` 共享底层数组：源存活期内可观察到源修改；源 slice 头被释放后，string 自身的指针仍是 GC 根，底层数组不应被回收。`string(b)` 则是独立副本。用例覆盖：源修改、释放源 slice/数组变量、先复制再丢弃视图、被调函数局部缓冲“脱离”后返回视图，以及缓冲复用下别名与副本的确定性差异。

# 期望行为

1. 零拷贝视图与复制 string 在 GC 后都等于原始内容；
2. 改写源字节后，零拷贝视图看到 `Z`/`Y`，复制版保持原值，二者确定性地不同；
3. 源 slice 与数组变量置 nil 并 GC 后，视图仍可读（视图保活底层数组）；
4. 先 `string(view)` 复制再丢弃视图，GC 后副本正确；
5. 被调函数内局部 `[]byte` 转成 string 返回后，GC 后内容正确；
6. 缓冲复用后别名视图等于新记录，复制版与复用前快照保持旧记录，GC 后仍稳定；
7. 最后一行 `ALL PASS l07_byte_to_string_lifetime`。

# 失败判读

- 视图在 GC 后内容错乱/efence 崩溃：string 的 Data 指针未被当作 GC 根，或底层数组被提前回收；
- 复制版随源变化：`string(b)` 被错误优化成别名；
- 别名视图不随源变化：转换意外复制，零拷贝失效；
- 快照在复用后被污染：快照实际是别名；
- 仅 stress/k8s 失败：写屏障/栈映射问题。

# 参数

- `UAF_SEED`：决定堆数组长度（64..95）与字节模式，失败信息带种子；
- 复用记录固定 28 字节，保证零拷贝 string 长度在复用后仍匹配新内容。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_byte_to_string_lifetime/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
