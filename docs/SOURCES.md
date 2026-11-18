# 三方库来源清单（41）

目标版本见《Go 编译器稳定性测试三方库清单（Go 1.24.1）》。研究方式：partial clone 只取 `*_test.go` / `testdata/`，产出 `docs/research/<lib>.md` 场景清单，用例用 stdlib 重写，**不逐行拷贝**。

- `状态`：`planned` / `fetched` / `mined`（已有 docs/research 场景表）/ `adapted`（已产出 cases）
- `阶段`：该库主要服务的用例批次（1=UAF 核心，2=并发/抢占/接口/unsafe，3=泛型/优化/初始化/模板，4=汇编/cgo/DWARF/多平台）

## A. Web / HTTP / RPC

| 库 | 版本 | 仓库 | 阶段 | 重点维度 | 状态 |
|---|---|---|---|---|---|
| Caddy | v2.10.0 | github.com/caddyserver/caddy | 2 | L02/L04/L06/L12 | adapted |
| frp | v0.67.0 | github.com/fatedier/frp | 2 | L02/L04/L06 | adapted |
| Gin | v1.10.0 | github.com/gin-gonic/gin | 2 | L02/L06/L11 | adapted |
| fiber | v2.52.6 | github.com/gofiber/fiber | 2 | L02/L06/L07/L12 | adapted |
| grpc-go | v1.71.1 | github.com/grpc/grpc-go | 2/3 | L02/L03/L10/L12 | adapted |
| kitex | v0.13.0 | github.com/cloudwego/kitex | 2/3 | L01/L02/L10/L12 | adapted |
| fasthttp | v1.59.0 | github.com/valyala/fasthttp | 2 | L05/L07/L11 | adapted |
| quic-go | v0.50.1 | github.com/quic-go/quic-go | 2 | L02/L03/L04/L15 | adapted |
| pion/webrtc | v4.1.2 | github.com/pion/webrtc | 2 | L02/L04/L06 | adapted |
| echo | v4.13.4 | github.com/labstack/echo | 2 | L02/L06/L11 | adapted |
| traefik | v3.3.5 | github.com/traefik/traefik | 2/3 | L02/L12/L15 | planned |

## B. 序列化 / 数据访问

| 库 | 版本 | 仓库 | 阶段 | 重点维度 | 状态 |
|---|---|---|---|---|---|
| sonic | v1.13.2 | github.com/bytedance/sonic | 1 | L05/L07/L08/L13/L17 | adapted |
| goccy/go-json | v0.10.5 | github.com/goccy/go-json | 1 | L05/L07/L11/L13 | adapted |
| gorm | v1.30.0 | github.com/go-gorm/gorm | 3 | L01/L06/L11 | adapted |
| pgx | v5.7.4 | github.com/jackc/pgx | 1 | L01/L02/L05/L13 | adapted |
| go-redis | v9.8.0 | github.com/redis/go-redis | 1 | L01/L02/L05/L13 | adapted |
| ent | v0.14.4 | github.com/ent/ent | 3 | L01/L06/L10 | adapted |
| protobuf-go | v1.36.6 | github.com/protocolbuffers/protobuf-go | 3 | L05/L06/L07/L10 | adapted |
| badger | v4.5.0 | github.com/dgraph-io/badger | 1 | L02/L05/L07/L13/L18 | adapted |

## C. 可观测性 / 基础设施

| 库 | 版本 | 仓库 | 阶段 | 重点维度 | 状态 |
|---|---|---|---|---|---|
| Kubernetes | v1.30.0 | github.com/kubernetes/kubernetes | 2 | L01/L02/L03/L04/L10 | planned |
| Terraform | v1.9.8 | github.com/hashicorp/terraform | 3 | L01/L06/L10/L12 | planned |
| Prometheus | v3.8.0 | github.com/prometheus/prometheus | 2 | L02/L03/L04/L18 | planned |
| etcd | v3.5.0 | github.com/etcd-io/etcd | 2 | L02/L04/L13 | adapted |
| TiDB | v8.5.4 | github.com/pingcap/tidb | 3 | L01/L02/L10/L13 | planned |
| MinIO | RELEASE.2025-04-22T22-12-26Z | github.com/minio/minio | 4 | L05/L07/L08/L09/L15 | planned |
| opentelemetry-go | v1.35.0 | github.com/open-telemetry/opentelemetry-go | 2 | L01/L02/L04/L15 | planned |
| nats-server | v2.11.0 | github.com/nats-io/nats-server | 1/2 | L02/L03/L05/L17/L19 | adapted |
| containerd | v2.0.4 | github.com/containerd/containerd | 2/4 | L02/L09/L15/L19 | planned |
| go-ethereum | v1.15.7 | github.com/ethereum/go-ethereum | 2/4 | L07/L08/L09/L13 | planned |
| restic | v0.18.0 | github.com/restic/restic | 2 | L02/L06/L10 | planned |
| consul | v1.20.5 | github.com/hashicorp/consul | 2 | L02/L03/L10/L15 | planned |
| gitea | v1.23.5 | github.com/go-gitea/gitea | 2/3 | L02/L06/L16 | adapted |
| tailscale | v1.80.2 | github.com/tailscale/tailscale | 2/4 | L02/L03/L09/L15 | adapted |

## D. 编译器 / 运行时极限

| 库 | 版本 | 仓库 | 阶段 | 重点维度 | 状态 |
|---|---|---|---|---|---|
| Hugo | v0.155.0 | github.com/gohugoio/hugo | 3 | L05/L06/L11/L16 | adapted |
| klauspost/compress | v1.17.11 | github.com/klauspost/compress | 1/4 | L05/L07/L08/L11 | adapted |
| goja | 2025-03 commit | github.com/dop251/goja | 2/3 | L03/L05/L11/L16 | planned |
| wazero | v1.9.0 | github.com/tetratelabs/wazero | 3/4 | L05/L07/L08/L15/L16 | planned |
| yaegi | v0.16.1 | github.com/traefik/yaegi | 3 | L01/L06/L10/L16 | adapted |
| golangci-lint | v2.0.2 | github.com/golangci/golangci-lint | 3 | L01/L10/L11/L16 | adapted |
| delve | v1.24.1 | github.com/go-delve/delve | 4 | L06/L14 | planned |
| sqlc | v1.28.0 | github.com/sqlc-dev/sqlc | 3 | L01/L10/L16 | adapted |

## 研究产物格式（docs/research/<lib>.md）

```markdown
# sonic v1.13.2
- 来源：research/repos/sonic（partial clone，仅 test 文件）
- 场景表：
  | 测试文件:行 | 场景 | 维度 | 复杂度 | 适配思路 |
  |---|---|---|---|---|
  | decode_test.go:120 | 大对象池复用+GC | L05/L18 | 中 | 用 sync.Pool + 不定长 payload 重写 |
- 推荐收录用例：...
```
