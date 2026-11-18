---
id: l15_suffix_edge_segments
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_suffix_edge_segments/m_LINUX.go:1 大写末段不是 GOOS
  - cases/L15_build_tags/l15_suffix_edge_segments/m_linux_AMD64.go:1 大写末段不是 GOARCH
  - cases/L15_build_tags/l15_suffix_edge_segments/m_linux_linux.go:1 重复 GOOS 段
  - cases/L15_build_tags/l15_suffix_edge_segments/m_windows_amd64_linux.go:1 GOARCH 在中间
  - cases/L15_build_tags/l15_suffix_edge_segments/m_linux_windows_amd64.go:1 三段中的 GOOS_GOARCH 成对后缀
  - cases/L15_build_tags/l15_suffix_edge_segments/m_js_wasm_amd64.go:1 倒数第二段是 GOARCH 而非 GOOS
  - cases/L15_build_tags/l15_suffix_edge_segments/m_linux_amd64_386.go:1 末段 386 单段匹配
  - cases/L15_build_tags/l15_suffix_edge_segments/main.go:52 期望 token 集合
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, filename-suffix, GOOS, GOARCH, case-sensitivity, parse-order, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

文件名后缀只按末尾 1~2 段解析，且大小写敏感。本用例覆盖解析边角：`_LINUX` /
`_AMD64` 大写段不匹配；`_linux_linux` 重复段；`_windows_amd64_linux` 中 GOARCH
位于中间不参与过滤；`_linux_windows_amd64` 的末两段构成合法 GOOS_GOARCH 对，必须
两者同时成立；`_js_wasm_amd64` 与 `_linux_amd64_386` 的倒数第二段是 GOARCH，只按
末段单段匹配。每个被选中文件在 init 登记 token，main 精确比对集合。

## 期望行为

1. 任何平台都选中 `m_LINUX`、`m_linux_AMD64`；
2. linux 另选 `m_linux_linux`、`m_linux_amd64_linux`、`m_arm_linux`、
   `m_windows_amd64_linux`；
3. windows+amd64 另选 `m_linux_windows_amd64`（成对后缀），其他 windows 不选；
4. amd64 另选 `m_386_amd64`、`m_unix_amd64`、`m_js_wasm_amd64`；386 另选
   `m_linux_amd64_386`；
5. 实际 init token 排序后与 `expectedTokens()` 完全相等（无多选、无漏选）；
6. GC 后集合不变；
7. 退出码 0，最后一行 `ALL PASS l15_suffix_edge_segments`。

## 失败判读

- 多出大写段文件：后缀匹配被错误地大小写折叠；
- 多出 `m_linux_windows_amd64`（linux）：成对后缀未要求 GOOS 同时匹配；
- 缺少 `m_linux_amd64_linux`：实现错误地让更早的 `_amd64` 段参与过滤；
- 缺少 `m_js_wasm_amd64` 或 `m_386_amd64`：把倒数第二段 GOARCH 误判为 GOOS；
- 期望集合与失败行一起打印，可直接对照。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_suffix_edge_segments
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
GOARCH=386 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.386 .
```
