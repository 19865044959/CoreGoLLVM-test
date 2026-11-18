# 长跑压测语料调研（stress corpus）

> 只读调研，未改 `cases/`、`docs/` 其他文件，未复制任何语料。对象：`research/repos/` 27 个 partial clone（工作树约 100 MiB，含 `.git` 约 170 MiB）与 `/mnt/d/work/Compiler/go`（HEAD `26a3c1f26e`）。
> 口径：仅“目录名恰为 `testdata`”的文件，不含 `.git` 与 `*_test.go`；大小为 MiB（1 MiB=1048576 B）。27 个本地克隆均未带 LICENSE；表内许可按上游仓库 LICENSE 于 2026-11-18 核对，拷贝前需复核逐文件归属。

## 结论

- testdata 共 **7434 文件 / 61.06 MiB**，其中 klauspost 41.11 MiB（67%）、sqlc 8.18、hugo 4.09、golangci-lint 3.07。
- 可直接作压测输入：klauspost 的 zip 压缩/解压集合、sonic/goccy JSON、etcd raft 文本轨迹、hugo 图片、Go 树的 TLS/trace 数据。
- 无落盘语料：badger、echo、frp、gorm、kitex、nats-server、webrtc；pgx 仅 4 个 48–159 B fuzz 种子。其“二进制/文本协议样本”主要存在于 `*_test.go`，只能提炼后合成，不能直接当语料库。
- 推荐默认：**小样本（≤2 MiB/场景）入库 + 大样本运行时按白名单读 + 合成数据兜底**；GPL 语料与含个人信息的样本不入库。

## 1. research/repos testdata 统计（按大小降序）

| 库 | 文件 | MiB | 主要扩展名/格式 | 上游 License | HEAD pin |
|---|---:|---:|---|---|---|
| klauspost-compress | 184 | 41.11 | zip(69)/zst/bin/txt/golden；压缩与 fuzz 语料 | BSD-3-Clause（gzhttp 为 Apache-2.0，s2/cmd 两目录为 MIT） | 72cd4a92a8 |
| sqlc | 5744 | 8.18 | go(2661)/sql(1886)/json(958)/yaml | MIT | 349137bd29 |
| hugo | 155 | 4.09 | jpg(66)/webp(28)/png/gif/tmpl | Apache-2.0 | 74c0bbfc6a |
| golangci-lint | 986 | 3.07 | go(470)/yml(470)/diff/json | **GPL-3.0-only** | 2b224c2cf4 |
| sonic | 10 | 1.52 | json/json.gz（7 个 gz） | Apache-2.0 | 28294cfa51 |
| gitea | 22 | 1.23 | git pack/idx、artifact chunk、png/webp/jpeg | MIT | 92f2d904f0 |
| protobuf-go | 98 | 1.20 | proto(46)/pb.go(51) | BSD-3-Clause | 3f79c52e7f |
| grpc-go | 91 | 0.25 | pem(56)/crl/key/proto | Apache-2.0 | cdbdb759dd |
| etcd | 27 | 0.14 | raft txt(23)/pem/db | Apache-2.0 | 946a5a6f25 |
| goccy-json | 1 | 0.11 | json.gz（code.json.gz） | MIT | 9872089c31 |
| tailscale | 11 | 0.06 | plg(4)/pem/json | BSD-3-Clause | c7a79d7bae |
| fiber | 14 | 0.05 | tmpl/html/pem/png/ico | MIT | e04f815c43 |
| gin | 6 | 0.01 | pem/proto/tmpl | MIT | 75ccf94d60 |
| quic-go | 6 | 0.01 | pem/key | MIT | b90058aba5 |
| caddy | 34 | <0.01 | txt(29)/php/html（Caddyfile/静态页） | Apache-2.0 | fb22a26b1a |
| ent | 8 | <0.01 | go schema | Apache-2.0 | 088bfd75ad |
| fasthttp | 1 | <0.01 | png | MIT | bb94b26bce |
| go-redis | 1 | <0.01 | go（vet testdata） | BSD-2-Clause | fba6dececd |
| pgx | 4 | <0.01 | pgproto3 fuzz 二进制种子 | MIT | 04bcc0219d |
| yaegi | 31 | <0.01 | go 解释器片段 | Apache-2.0 | 3fbebb3662 |
| badger | 0 | 0 | —（KV 全在测试内生成） | Apache-2.0 | bb576b6d2d |
| echo | 0 | 0 | —（inline 字符串） | MIT | 98ca08e7dd |
| frp | 0 | 0 | — | Apache-2.0 | 5f575b8442 |
| gorm | 0 | 0 | — | MIT | c44405a25b |
| kitex | 0 | 0 | — | Apache-2.0 | 1dd7526b37 |
| nats-server | 0 | 0 | —（NATS 协议字面量在 `server/*_test.go`） | Apache-2.0 | 99e836e9af |
| webrtc | 0 | 0 | — | MIT | f5d98ceac9 |
| **合计** | **7434** | **61.06** | | | |

## 2. 最大的 20 个 testdata 文件（路径相对 `research/repos/`）

| # | 路径 | MiB | 形态/价值 |
|---:|---|---:|---|
| 1 | klauspost-compress/s2/testdata/fuzz/block-corpus-raw.zip | 8.03 | 834 个 raw block，解压 36.6 MiB；s02 解压/环形缓冲 |
| 2 | klauspost-compress/zstd/testdata/decoder.zip | 6.61 | 188 项（zst+raw），解压 12.0 MiB；s02 |
| 3 | klauspost-compress/zstd/testdata/fuzz/decode-corpus-raw.zip | 4.72 | 8002 项，解压 6.4 MiB；恶意/截断输入 |
| 4 | klauspost-compress/s2/testdata/fuzz/block-corpus-enc.zip | 3.04 | 143 项，解压 10.1 MiB；s02 |
| 5 | klauspost-compress/zstd/testdata/fuzz/decode-corpus-encoded.zip | 2.13 | 1185 项，解压 4.1 MiB；s02 |
| 6 | sqlc/internal/endtoend/testdata/process_plugin_disabled/gen/codegen.json | 2.02 | 生成代码模型 JSON；s05 形态参考 |
| 7 | sqlc/internal/endtoend/testdata/codegen_json/gen/codegen.json | 2.02 | 与 #6/#8 近似重复，取一份 |
| 8 | sqlc/internal/endtoend/testdata/process_plugin_sqlc_gen_json/gen/codegen.json | 2.02 | 同上 |
| 9 | klauspost-compress/zstd/testdata/fuzz/encode-corpus-raw.zip | 1.57 | 2085 项，解压 3.6 MiB；s02 |
| 10 | klauspost-compress/zstd/testdata/regression.zip | 1.56 | 36 项，解压 3.6 MiB；回归正确性 |
| 11 | klauspost-compress/zstd/testdata/fuzz/encode-corpus-encoded.zip | 1.55 | 980 项，解压 11.2 MiB；s02 |
| 12 | klauspost-compress/zstd/testdata/comp-crashers.zip | 1.44 | 1657 项，解压 2.9 MiB；畸形输入 |
| 13 | klauspost-compress/flate/testdata/fuzz/FuzzEncoding.zip | 1.16 | 390 项，解压 5.7 MiB；s02 |
| 14 | gitea/services/gitdiff/testdata/academic-module/objects/pack/pack-597efbc3613c7ba790e33b178fd9fc1fe17b4245.pack | 1.11 | 真实 git pack；s02 大块二进制 |
| 15 | klauspost-compress/s2/testdata/random | 1.00 | 1 MiB 随机字节；s02 |
| 16 | golangci-lint/pkg/result/processors/testdata/exclusion_generated_file_filter/exclude_long_line.go | 0.95 | 单行约 1 MB Go；s05 长行/扫描器（GPL，勿拷） |
| 17 | klauspost-compress/zstd/testdata/benchdecoder.zip | 0.88 | 12 项，解压 0.9 MiB；s02 |
| 18 | klauspost-compress/zstd/testdata/headers.zip | 0.75 | 7797 个极小 header，解压 0.1 MiB；s06 小对象流 |
| 19 | klauspost-compress/flate/testdata/fuzz/encode-raw-corpus.zip | 0.65 | 1995 项，解压 0.7 MiB；s02 |
| 20 | golangci-lint/scripts/gen_github_action_config/testdata/all-releases-v2.json | 0.64 | 版本元数据 JSON；s01/s05（GPL，勿拷） |

Top20 之外的高价值小件：sonic `twitter.json` 617 KiB、`twitterescaped.json` 550 KiB、7 个 gz JSON（解压共 7.5 MiB）；goccy `code.json.gz` 118 KiB（解压 1.85 MiB，与 sonic `golang_source.json.gz` 同源）；etcd raft `*.txt` 23 个共 91 KiB；protobuf-go 46 个 `.proto`；Go 树 `crypto/tls/testdata` 1.7 MiB 与 3 个 trace stress 共 1.0 MiB。

## 3. /mnt/d/work/Compiler/go 可复用部分（BSD-3-Clause + PATENTS）

| 位置 | 体量 | 形态 | 压测用途 |
|---|---|---|---|
| test/stress/{runstress,parsego,maps}.go | 3 文件 9.6 KiB | 可运行的 runtime soak 工具 | s03/s04 形态参考；改写而非原样拷 |
| test/fixedbugs/bug257.go | 2.9 MiB | 超长 Unicode 字符串常量 | s05 大 rodata/常量；bug385_64 为 16480 个全局数组，可作链接形态参考 |
| test/codegen/*.go + src/cmd/compile/internal/test/testdata | 95 文件 584 KiB + 60 文件 1.3 MiB | 编译器/生成代码模式 | s05 大 struct/方法集/switch 参考 |
| test/stack*.go、gc*.go、escape*.go | 43 文件 236 KiB | 栈/GC/逃逸回归源码 | s03/s04 模式库，不直接运行 |
| src/runtime/testdata（testprog/testprogcgo） | 209 文件约 1 MiB | 可运行程序：gc、schedmetrics、gomaxprocs、deadlock | s02–s04 行为模板 |
| src/internal/trace/internal/tracev1/testdata/stress_1_* | 3 文件 1.0 MiB | 二进制执行轨迹 | s06 不透明负载/事件流 |
| src/crypto/tls/testdata | 130 文件 1.7 MiB | TLS 握手 transcript+测试证书 | s01 握手/服务压测 |
| src/testdata/Isaac.Newton-Opticks.txt + src/compress/flate/testdata | 554 KiB + 69 文件 284 KiB | 大文本 + deflate 输入/golden | s02 压缩/解压语料（Newton 为公版文本） |
| src/image/testdata | 41 文件 444 KiB | png/jpeg/gif | s02 图片解码/重编码 |
| src/go/{parser,printer}/testdata | 34+44 文件 316 KiB | Go 源码/golden | s05 生成源码解析 |
| misc/ + lib/time/zoneinfo.zip | 约 1 MiB + 399 KiB | cgo/wasm/android 样例；时区库 | 压测复用价值低；zoneinfo 仅供 s06 定时器可选 |

## 4. 复用方式评估

| 方式 | 体积/交付 | 许可/隐私 | 适用 |
|---|---|---|---|
| 直接拷入 `stress/testdata` | 建议单场景 ≤2 MiB、全仓 ≤5 MiB；随 git 分发 | 仅 MIT/BSD/Apache 且逐文件归属清楚；保留 LICENSE/NOTICE/版权行/上游 URL/commit；GPL 与含个人信息样本禁用 | 小型 JSON 种子、etcd raft txt、测试证书、公版文本；适合自包含 CI 冒烟 |
| 运行时读 `research/repos` | 仓库零增长；语料 61 MiB，工作树 100 MiB，含 `.git` 170 MiB；`research/repos` 已被 gitignore，内网需另打 tarball（不能靠重新 clone） | 不复制则无再分发问题，但仍需合规使用 | 本地/夜间长跑；用 `STRESS_CORPUS_DIR` 白名单 + manifest，缺失时回退合成 |
| 脚本生成同形数据 | 代码极小，无大文件 | 无第三方许可/隐私风险；固定 seed 可复现 | s01 JSON 报文、s02 随机/可压 buffer、s05 生成结构、s06 事件流；推荐作为默认与 CI 兜底 |

## 5. 按场景推荐（s01–s06）

| 场景 | 首选语料 | 推荐用法 | 替代/注意 |
|---|---|---|---|
| s01_service_soak（服务） | sonic twitter/synthea/golang_source、goccy code.json.gz、Go `crypto/tls/testdata`、protobuf `.proto` | 启动时解压，HTTP/JSON handler 持续解析并校验 checksum；TLS 用测试证书 | 推文/合成病历不原样入库；按同 schema 生成报文。TLS 测试私钥是公开测试键，可拷但须标注 |
| s02_heap_cycle_soak（堆） | klauspost s2/zstd/flate zip、hugo 图片、gitea pack | 运行时白名单解压 2–3 个 zip（最大 36.6 MiB），大 byte slice 环形保留 + FreeOSMemory；图片 decode/encode | 不入库 41 MiB；替代为 Go flate 语料 + Newton 文本 + 合成随机/可压 buffer；zstd 内层在 stdlib 无解码器，只能当 opaque 负载 |
| s03_stack_churn_soak（栈） | 无文件语料 | 参考 `test/stress/parsego.go`、`testprog/gc.go`、stack/escape 测试，生成深递归/大栈帧/panic-recover | 不拷贝测试代码；用程序按深度/闭包/帧大小参数化生成 |
| s04_preempt_storm_soak（抢占） | 无文件语料 | 参考 `test/stress/runstress.go`、`runtime/race/testdata` 的并发热循环形态，生成紧循环+校验和+GC churn | 语料不适用；固定 GOMAXPROCS/seed，watchdog 检测停滞 |
| s05_generated_code_soak（生成代码） | protobuf-go `.proto`/`.pb.go`、sqlc 生成 Go/`codegen.json`、Go parser/printer、golangci 长行 | 取 schema/结构形态，生成大 struct/方法集/switch 表并持续 marshal/unmarshal | golangci 为 GPL-3.0、sqlc 8.2 MiB，均不入库；用生成器按形态重写 |
| s06_queue_pipeline_soak（流水线） | etcd raft `*.txt`、Go trace stress、NATS 协议字面量、tailscale `matches-1.json` | 生成带序号消息流/状态机命令，多级 channel+背压+取消；用 etcd 轨迹做期望状态参照 | NATS 无 testdata，从 `server/client_test.go` 提炼帧格式后合成；trace 可当 opaque 负载 |

## 6. 风险与规避

- **许可证**：golangci-lint 为 GPL-3.0-only，其 testdata 也勿拷入；klauspost 是“默认 BSD-3 + gzhttp Apache-2.0 + s2/cmd 两目录 MIT”的多许可；仓库 LICENSE 不自动覆盖第三方 testdata（尤其 sonic 的 twitter/synthea、klauspost fuzz 语料）。
- **隐私/归属**：gitea academic-module 的 git pack 含提交作者姓名/邮箱；sonic twitter 含用户推文；synthea 为合成病历但仍具患者形态；均不得原样入库，需去元数据或合成。
- **体积**：61 MiB 语料、41 MiB 集中于 klauspost，zip 解压峰值 36.6 MiB；一律不 `git add`，大件走运行时分发。
- **畸形语料**：fuzz/comp-crashers 故意 malformed，仅用于健壮性，不可用于 roundtrip/正确性断言；场景中须标注 fuzz-only。
- **可获取性与复现**：partial clone 无 LICENSE、`research/repos` 被 gitignore、离线内网无法重新 clone；必须 pin HEAD、带 LICENSE 与 manifest；CI 缺失语料时回退合成。
- **确定性**：文件遍历排序、固定 seed、不依赖 mtime；Go 树路径不可硬编码，优先 `STRESS_CORPUS_DIR`/`GOROOT` 探测。
- **覆盖缺口**：badger/nats/webrtc 无 testdata，pgx 仅 4 个种子；“二进制协议覆盖”只能由测试代码提炼并合成的帧代表，不能宣称真实协议全覆盖。
