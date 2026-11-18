# gitea v1.23.5

- 来源：`research/repos/gitea`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/gitea`
- 本地核验：`git describe` = `v1.23.5`，HEAD `92f2d90`（2025-03-04，`Upgrade golang net from 0.35.0 -> 0.36.0 #33795 (#33796)`）
- 模块：`code.gitea.io/gitea`，`go 1.23.6`；仓库内 779 个 `*_test.go`。
- 范围：按 `template|FuncMap|Expand|Expr|escape|init|config|render` 筛选后精读约 16 个文件，重点为 `modules/templates/{eval,vars,scopedtmpl}`、`modules/templates/{helper,util,htmlrenderer}_test.go`、`modules/setting/config_*_test.go`、`models/renderhelper`、`modules/markup/sanitizer*`。文件:行均相对本仓根目录。
- 说明：Gitea 的模板函数、markup 渲染器大量依赖三方库与仓库 fixture；可迁移的是表达式求值、变量展开、模板作用域/转义、注册表与配置分层语义，用例用 stdlib 重写。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `modules/templates/eval/eval_test.go:21` | 后缀表达式求值器：`Expr(tokens...)` 接收 token 序列，返回带类型值；`:32` 用例表 | L16 | 高 | 自写 shunting-yard：操作数栈/运算符栈 + 括号/函数调用/逗号分隔 |
| `.../eval/eval_test.go:44` | 函数调用 `sum(1,2,3,4)`；嵌套表达式 `100 + sum(1,2+3,0.0)/2` | L16 | 中 | 识别 `name(` 起函数帧，收集逗号分隔子表达式 |
| `.../eval/eval_test.go:43` | int 与 float 混合提升：`(1+2.0)/3`→float64；`:45`/`:46` 复合常量表达式 | L11 | 中 | 用 `go/constant`/自写数值类型提升规则，常量表达式在求值期折叠 |
| `.../eval/eval_test.go:59` | `and`/`or` 使用模板真值语义（非零即真），`:60`–`:63` 真值组合 | L16/L11 | 低 | 自写 `toBool`；逻辑运算符返回操作数并短路 |
| `.../eval/eval_test.go:81` | 错误路径：`0/0`、缺操作数、未闭合括号、多余值、未知运算符（至 `:88`） | L16 | 中 | 每种栈状态返回固定错误串；表驱动断言 |
| `modules/templates/vars/vars_test.go:12` | `{a}` 花括号变量展开；`:36` 中文变量名；`:45` 未闭合 `{{a}` 部分展开并报错；`:54` 孤立 `}`/`{` | L16 | 中 | 单遍字节扫描器：识别 `{ident}`，非法花括号保原文并记录首个错误 |
| `modules/templates/scopedtmpl/scopedtmpl_test.go:17` | 同一模板集合按 executor 注入不同 `CtxFunc`，两个 goroutine 并发渲染互不污染 | L16/L02 | 高 | 每次执行复制 `FuncMap`；`template.Clone` + 独立执行器，`sync.WaitGroup` |
| `.../scopedtmpl_test.go:73` | 嵌套 `base` 模板的 html/template URL 上下文转义（`?q=/`→`%2f`） | L16 | 中 | 直接使用 stdlib `html/template`；验证嵌套模板共享转义上下文 |
| `.../scopedtmpl_test.go:91` | URL 中出现 `{{if}}` 分支导致 “ambiguous context” 解析错误 | L16 | 中 | html/template 解析期上下文检查；固定错误子串 |
| `modules/templates/helper_test.go:68` | 模板真值矩阵：nil/bool/数值/复数/chan/func/各级指针/array/slice/map/struct（`:73` 起） | L16/L06 | 中 | `reflect` 逐 Kind 判定；指针递归解引用；与 template `if` 输出对齐 |
| `.../helper_test.go:16` | 邮件主题/正文分隔线识别（多行 `---`、前后空格、最短长度） | L16 | 中 | 字节扫描 + 行首/行尾判定状态机，边界表驱动 |
| `.../helper_test.go:60` | `jsEscapeSafe`：`&<>'"` → `&<...` | L16 | 低 | 字节映射表，固定输出 |
| `modules/templates/htmlrenderer_test.go:18` | 根据模板错误行/列在源码上画 `^^^` 指示；`:56` 各类 parse 错误格式化 | L16 | 中 | 行拆分 + 列偏移计算 + 下划线填充；`:56` 需临时文件，改为内存字符串 |
| `modules/templates/util_test.go:15` | `dict` FuncMap：奇偶参数、`.` 合并 map、nil 忽略、非法参数报错 | L16/L06 | 中 | 变参 `...any` + `reflect` 判断 map 类型；结果 map 键排序输出 |
| `.../util_test.go:48` | `SliceUtils.Contains`/`StringUtils.Contains` 作为模板方法调用；错误类型报 `invalid type` | L06/L16 | 中 | 反射取 slice/array/string，逐元素比较；失败返回确定错误 |
| `modules/templates/util_date_test.go:17` | 日期格式化/相对时间 FuncMap（`:53` TimeSince） | L16 | 低 | `time.Format` + 固定时区/基准时间，避免 now 不确定性 |
| `modules/setting/config_env_test.go:13` | `GITEA__SEC__KEY` 环境变量名解码（大小写、`0x2E` 转 `.`） | L12 | 低 | 前缀/双下划线分割 + 十六进制转义解码 |
| `.../config_env_test.go:35` | `__FILE` 后缀读文件注入配置；非法组合拒绝 | L12 | 中 | 环境变量→配置层合并；`__FILE` 用 `os.ReadFile`，测试用 `t.TempDir` |
| `.../config_env_test.go:78` | `EnvironmentToConfig` 把环境变量覆盖到已有配置，返回 changed 标志 | L12 | 中 | 两层 map 合并；幂等性与 changed 判定 |
| `modules/setting/config_provider_test.go:13` | INI 子节继承父节 key 的怪异行为；`:45` helper 读取语义；`:79` 文件加载/保存 | L12 | 中 | 自写两级 section map + 继承链；文件读写用临时目录 |
| `models/renderhelper/main_test.go:14` | `TestMain` 中调用 `markup.Init(&RenderHelperFuncs{...})` 注册回调后再跑测试 | L12 | 中 | 包级注册表 + `init`/显式 Init 注入函数指针；顺序断言 |
| `modules/markup/sanitizer_default_test.go:13` | HTML 白名单清洗（属性/协议/class） | L16 | 高 | 依赖 bluemonday，不可直接移植；只可借“允许列表”思想用 `golang.org/x/net/html` 重写（仍属三方） |
| `modules/markup/html_internal_test.go:70` | 问题号 `#123` 渲染、`@mention` 识别（至 `:101`） | L16 | 高 | 依赖 Gitea markup/DB/repo 上下文；仅借正则+占位替换思路 |

## 推荐收录（14 条）

1. `l16_gt_expr_eval` — `modules/templates/eval/eval_test.go:21`、`:44`。shunting-yard 表达式求值（含函数调用）；P1，L16。
2. `l11_gt_expr_const` — `.../eval/eval_test.go:43`、`:45`。int/float 常量提升与折叠；P1，L11。
3. `l16_gt_expr_errors` — `.../eval/eval_test.go:81`。栈状态错误分类；P1，L16。
4. `l16_gt_vars_expand` — `modules/templates/vars/vars_test.go:12`。花括号变量扫描、Unicode、部分展开；P1，L16。
5. `l16_gt_scoped_funmap` — `modules/templates/scopedtmpl/scopedtmpl_test.go:17`。并发执行器隔离 FuncMap；P2，L16/L02。
6. `l16_gt_escape_context` — `.../scopedtmpl_test.go:73`、`:91`。嵌套模板 URL 转义与 ambiguous context；P1，L16。
7. `l06_gt_truthy` — `modules/templates/helper_test.go:68`。全类型真值矩阵；P1，L06/L16。
8. `l16_gt_dict` — `modules/templates/util_test.go:15`。变参 dict 合并/类型检查；P1，L16/L06。
9. `l06_gt_reflect_utils` — `.../util_test.go:48`。反射实现 slice/string Contains；P1，L06。
10. `l16_gt_error_line` — `modules/templates/htmlrenderer_test.go:18`。错误行列→`^^^` 指示；P1，L16。
11. `l12_gt_env_config` — `modules/setting/config_env_test.go:78`（`:35` 文件注入）。环境变量覆盖配置、幂等；P2，L12。
12. `l12_gt_config_sections` — `modules/setting/config_provider_test.go:13`。section 继承与查找语义；P2，L12。
13. `l12_gt_markup_init` — `models/renderhelper/main_test.go:14`。显式注册回调再初始化；P2，L12。
14. `l16_gt_subject_split` — `modules/templates/helper_test.go:16`。多行分隔线扫描状态机；P1，L16。

## 不可移植清单

- `tests/integration/**`：需要运行中的 Gitea 服务、数据库、git 仓库与 fixture，无法单包运行。
- `models/**`：多数经 `unittest.MainTest` 加载 YAML fixture 并连数据库。
- `modules/templates/util_render_test.go`：`TestMain` 调 `unittest.InitSettings` + `git.InitSimple`，且依赖真实仓库路径与 `translation.MockLocale`。
- `modules/markup/**`：markdown/orgmode/sanitizer 依赖 goldmark、chroma、bluemonday 等三方库及仓库上下文。
- `modules/templates/helper_test.go:64` `SanitizeHTML`、`sanitizer_default_test.go:13`：白名单清洗由 bluemonday 实现，标准库无等价物。
- `htmlrenderer_test.go:56`：写临时模板文件并通过 `assetfs.Layered` 读取，属文件系统副作用（`:18` 的纯字符串行定位可移植）。
- `modules/setting/config_provider_test.go:79`：依赖第三方 ini 解析器对子节继承/文件保存的既有行为。
- 所有模板资源加载（`templates/**`、`options/**`、`go:embed`）：依赖完整仓库目录树。
- `modules/sitemap` 等 model 驱动渲染：依赖实体数据，不属模板语义用例。
