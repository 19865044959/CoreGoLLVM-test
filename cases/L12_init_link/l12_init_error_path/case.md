---
id: l12_init_error_path
dim: L12_init_link
priority: P1
sources:
  - gitea@v1.23.5: modules/setting/config_env_test.go:78 环境变量到配置的加载与错误处理
  - gitea@v1.23.5: modules/setting/config_provider_test.go:13 section 继承/查找与错误键覆盖语义
tags: [L12, init-error, recover, fallback, deterministic, multi-file, gc]
requires: []
runtime: "<10s"
---

# 场景

两个跨文件 init：加载 init 解析固定 fixture，遇到负值条目返回可分类错误而不是 panic，保留失败前缀；兜底 init 补齐缺失默认项，并用 defer/recover 把可选模块的 panic 收敛为错误。跨文件 init 顺序未定义，用例验证两种顺序都收敛到同一确定表状态。

# 期望行为

1. 两个 init 都执行了（各出现一次），加载错误未中断后续 init；
2. 加载错误 `errors.Is(errBadValue)` 成立，文本为 `entry 2 "beta": invalid value: -5`；
3. 可选模块错误文本为 `recovered: optional module broken`，兜底 init 自身无错误；
4. 最终表为 `alpha=1,beta=2,gamma=3`（alpha 来自失败前前缀，beta/gamma 来自兜底），与 init 顺序无关；
5. 失败状态下仍可做错误分类：重复名 `errDuplicateKey`、负值 `errBadValue`、空名 `errEmptyKey`，且被拒绝的 put 不改变表；
6. 修正后的 fixture 可正常加载为 `delta=4,epsilon=5`；
7. GC + 垃圾分配后表内容、条目指针与错误文本不变，init 不会被重放；
8. 最后一行 `ALL PASS l12_init_error_path`。

# 失败判读

- 进程 panic/退出非 0：init 内错误未收敛，或 recover 路径实现错误；
- 最终表为 2 项或 beta=0：兜底未执行/被错误中断；
- 错误文本不匹配：错误包装（`%w`）或错误分类实现错误；
- init 顺序不同导致结果不同：兜底逻辑依赖了未定义的跨文件顺序；
- GC 后表变化：全局表指针扫描错误。

# 参数

无随机路径；跨文件 init 顺序未定义，断言与顺序无关，`UAF_SEED` 不参与。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_error_path/*.go
GODEBUG=clobberfree=1 /tmp/case
```
