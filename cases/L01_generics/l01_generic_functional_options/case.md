---
id: l01_generic_functional_options
dim: L01_generics
priority: P0
sources:
  - webrtc@v4.1.2: api_test.go:15 TestNewAPI 构造后默认 settingEngine/mediaEngine/interceptorRegistry 均非 nil
  - webrtc@v4.1.2: api_test.go:22 TestNewAPI_Options WithSettingEngine 覆盖默认字段
  - webrtc@v4.1.2: api_test.go:35 TestNewAPI_OptionsDefaultize 传入 nil 注入时回落到默认对象
tags: [L01, generics, functional-options, defaults, nil-option, option-chain, config, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

自建泛型 functional options：`Option[T any] = func(*T)`、`applyOptions[T]`、`chainOptions[T]`，同一套 combinator 同时作用于 `serverConfig` 与 `poolConfig`。语义与 pion/webrtc `NewAPI(WithSettingEngine, WithMediaEngine(nil))` 对齐：nil 选项被忽略、默认值只填零值、后写选项覆盖先写选项、nil 注入后由默认化选项回落。

UAF_SEED 驱动 18–26 个随机选项（地址、连接数、重试、TLS、tag、hook、defaults、nil、选项链）的排列，参考实现逐 kind 独立计算期望配置，再与泛型 apply 的结果比较；显式 GC 后核对 hook 指针与 blob、tag 顺序。固定用例覆盖 nil 选项、默认值不覆盖显式值、选项链、跨类型实例化与校验错误计数。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_functional_options`；
2. nil Option 被跳过且无副作用；后出现的 WithAddr/WithMaxConns 覆盖先出现的值；
3. withServerDefaults 只填充零值字段，不覆盖显式非零值；
4. chainOptions 按顺序应用且可包含 nil，tag 追加顺序稳定；
5. 随机选项序列的最终 (addr, maxConns, retries, tls, tags, hook 指针) 与参考模型完全一致；
6. 显式 GC 后 hook 指针身份、blob 与 tag 字符串不变；
7. `Option[poolConfig]` 作用于另一配置类型，name/size/labels 正确；
8. 校验函数对合法配置返回 0 个错误，对空 addr、负数 maxConns/retries、重复 tag 返回 4 个错误。

# 失败判读

- `random option result mismatch`：选项闭包与参考 kind 语义分叉，或 nil 判断缺失；
- `hook pointer mismatch` / `payload corrupted`：配置中的指针未被正确传递或保活；
- `later option did not win`：apply 顺序被反转或默认化在覆盖之后执行；
- `validation error count`：校验规则与声明不一致；
- `SIGSEGV` / `fatal error: found bad pointer`：闭包捕获的指针在 GC 中被破坏（偏 L18）。

# 参数

- `UAF_SEED`：决定随机选项个数、kind、参数与 hooks 内容；失败输出带种子；
- 无 TORTURE 参数（P0，单次 <10ms）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_functional_options/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
