---
id: l01_generic_csv_codec
dim: L01_generics
priority: P0
sources:
  - gitea@v1.23.5: modules/csv/csv_test.go:34 TestCreateReaderAndDetermineDelimiter 逗号/分号/制表符与空字段的表驱动解析
  - gitea@v1.23.5: modules/csv/csv_test.go:304 TestGuessDelimiter 根据首行引号外的候选分隔符计数猜测
  - gitea@v1.23.5: services/gitdiff/csv_test.go:18 TestCSVDiff 表格 diff 依赖稳定的 CSV 行列解析
tags: [L01, generics, csv, encoding-csv, delimiter-detection, quoting, roundtrip, errors, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

自建泛型 `Codec[T any]`：`Header []string` + `ToRecord func(T) []string` + `FromRecord func([]string) (T,error)`，基于 `encoding/csv` 实现 Encode/Decode；Decode 读取全部字节后扫描首行（引号外）统计 `,` `;` `\t` 出现次数以猜测分隔符，再按 `FieldsPerRecord=len(Header)` 严格解码。对应 gitea `modules/csv` 的分隔符猜测与 gitdiff 表格解析。

实例化 `Codec[csvPerson]` 与 `Codec[csvPoint]`。UAF_SEED 驱动 20–31 行 person 数据，note 字段包含逗号、引号、换行、空串，部分行带 tag；编码后显式 GC，再进行两轮解码并逐字段比对。固定用例覆盖分号+CRLF+空字段、制表符分隔且字段内含逗号、点表分号往返，以及 4 类错误路径（编码字段数不匹配、解码字段数不匹配、age 非数字、header 不匹配）。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_csv_codec`；
2. 逗号 CSV 往返后行数、name/role/age/note/tags 与输入逐项一致，分隔符探测结果为 `,`；
3. note 中的逗号、双引号与换行被 csv 正确加引号/还原；空 note 与 0 个 tag 保持为空；
4. 显式 GC 后第二次解码结果仍与原始行一致（字符串与 tags 切片未被破坏）；
5. 分号样例（含引号内分号、空字段、CRLF）探测为 `;` 且 3 行内容正确；
6. 制表符样例探测为 `\t`（字段内逗号不导致误判），role 中的逗号原样保留；
7. `Codec[csvPoint]` 用分号编码后自动探测并往返一致；
8. 4 类错误输入全部返回非 nil error，错误计数恰好为 4。

# 失败判读

- `delimiter detection`：引号状态机未跳过引号内候选分隔符，或平局优先级变化；
- `row N roundtrip mismatch`：ToRecord/FromRecord 不对称，或 FieldsPerRecord 设置错误；
- `post-GC row mismatch`：解码结果依赖了被回收的输入缓冲（未拷贝/未保活）；
- `error path count`：错误被吞掉或合法输入被误拒；
- `SIGSEGV` / `fatal error: found bad pointer`：栈映射漏标或写屏障缺失。

# 参数

- `UAF_SEED`：决定 person 行数、年龄、tag 数与内容；失败输出带种子；
- 无 TORTURE 参数（P0，单次 <10ms）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_csv_codec/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
