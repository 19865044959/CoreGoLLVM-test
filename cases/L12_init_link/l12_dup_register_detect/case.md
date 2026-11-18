---
id: l12_dup_register_detect
dim: L12_init_link
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 同名 compressor 重复注册
  - golangci-lint@v2.0.2: pkg/lint/lintersdb/validator_test.go:84 重复/非法配置校验错误
  - ent@v0.14.4: entc/gen/globalid_test.go:72 冲突注册表检测与合并
tags: [L12, duplicate-detection, registry, panic, error, case-insensitive, atomicity, gc]
requires: []
runtime: "<10s"
---

# 场景

两个 init 共注册 3 个内置插件。主程序验证：同名（含大小写不同）重复注册返回 `errDuplicate` 且不覆盖旧值；空名返回 `errEmptyName`；冻结后任何注册返回 `errFrozen`；路由表的重复 pattern 与 nil handler 分别以 panic 拒绝，recover 后表状态不变且可继续注册。

# 期望行为

1. init 后注册表恰有 3 项，排序为 `gob,json,text`，`json` 的 factor 与 payload 正确；
2. 注册 `json`/`JSON` 均返回 `errDuplicate`，`lookup("json")` 仍为原指针、factor 不变、表长度不变；
3. 空名返回 `errEmptyName`；失败的 add 不改变表；
4. `yaml` 注册成功；`YAML` 返回 `errDuplicate`，表仍指向首次注册的指针；
5. 冻结后新增 `xml` 与重复注册 `json` 都返回 `errFrozen`，表长度不变；
6. 重复路由 `/a` panic 出 `*routePanic`，pattern/kind/文本正确，表仍为 1 项且调用原 handler 只命中旧实现；
7. nil handler panic 出哨兵 `errNilHandler`，不插入路由；recover 后可正常注册 `/b`；
8. GC 后插件指针、payload 与路由表均有效；最后一行 `ALL PASS l12_dup_register_detect`。

# 失败判读

- 重复注册返回 nil 或覆盖旧值：重复检测或原子性实现错误；
- 大小写变体未被识别：名称规范化错误；
- 冻结后仍可注册：冻结检查缺失或顺序错误；
- 重复路由不 panic/panic 类型错误：冲突检测路径错误；
- panic 后表多出一项：失败路径发生了部分写入；
- GC 后指针/数据损坏：全局注册表扫描错误。

# 参数

- `UAF_SEED`：决定最后轮换读取的起始位置；断言与起点无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_dup_register_detect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
