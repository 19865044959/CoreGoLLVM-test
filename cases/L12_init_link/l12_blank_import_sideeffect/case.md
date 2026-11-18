---
id: l12_blank_import_sideeffect
dim: L12_init_link
priority: P1
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 所有 testdata 包以 blank import 触发 init 注册
  - grpc-go@v1.71.1: clientconn_parsed_target_test.go:60 init() 中 resolver.Register 注册副作用
tags: [L12, blank-import, package-init, side-effect, image/png, net/http/pprof, registry, gc]
requires: []
runtime: "<10s"
---

# 场景

`imports.go` 以空白标识符导入 `image/png` 与 `net/http/pprof`。这两个标准库子包在各自 init 中向进程级注册表写入副作用：PNG 解码器注册到 `image`，pprof handler 注册到 `http.DefaultServeMux`。本包的包级变量 `pngFormatAtInit`/`pprofPatternAtInit` 在导入包初始化完成后求值，主程序验证副作用可见、可解码固定 1x1 PNG、pprof 路由存在，并在并发解码 + GC 后复核。

# 期望行为

1. `pngFormatAtInit==true`、`pprofPatternAtInit` 含 `/debug/pprof/`：空白导入包的 init 早于本包变量初始化；
2. PNG fixture 可被解码，而带 JPEG magic 的输入返回 `image.ErrFormat`（未导入 `image/jpeg`）；
3. `image.DecodeConfig` 返回 format `png`、1x1；完整解码得到 1x1 不透明红色像素；
4. `/debug/pprof/`、`cmdline`、`profile`、`symbol`、`trace`、`goroutine` 均查到非空 pattern，未注册路径 pattern 为空；
5. pprof 首页返回 200 且正文包含 `Types of profiles available`；
6. 8 goroutine 并发解码 + 主 goroutine GC 期间结果正确；
7. GC 后 PNG 注册、pprof 路由、解码结果均不变；
8. 最后一行 `ALL PASS l12_blank_import_sideeffect`。

# 失败判读

- 初始化期标志为 false：空白导入包的 init 未执行或晚于本包变量初始化；
- `image.DecodeConfig` 报 `unknown format`：PNG 解码器注册副作用丢失；
- pprof pattern 为空：pprof init 未写 DefaultServeMux；
- pprof 首页状态/正文错误：handler 注册到了错误 mux 或未执行；
- 并发阶段失败：解码器注册表被并发修改（init 后应只读）；
- GC 后注册丢失：全局注册表指针扫描错误。

# 参数

- `UAF_SEED`：决定并发阶段轮换检查 pprof 路由的起点；断言与起点无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_blank_import_sideeffect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
