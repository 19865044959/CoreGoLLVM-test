---
id: l15_tag_excluded_unparsed
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_excluded_unparsed/bad_syntax.go:1 语法错误的被排除文件
  - cases/L15_build_tags/l15_tag_excluded_unparsed/bad_import.go:1 缺失 import 的被排除文件
  - cases/L15_build_tags/l15_tag_excluded_unparsed/bad_package.go:1 不同 package 名的被排除文件
  - cases/L15_build_tags/l15_tag_excluded_unparsed/main.go:52 base 实现校验
  - cases/L15_build_tags/l15_tag_excluded_unparsed/main.go:54 被排除文件 init 计数
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, file-selection, parse-order, duplicate-symbol, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

`//go:build` 的排除发生在解析包体与 import 图之前。目录中放置三个由永不成立
tag 排除的"炸弹"文件：语法错误（且有重复符号）、导入不存在的包、声明了不同
package 名。只要构建成功，就说明这些文件没有被解析；init 计数再验证运行期
也没有它们。

## 期望行为

1. 构建成功（三个炸弹文件均被排除）；
2. `baseImpl()=="base"`、`baseConst==7`；
3. `baseInits==1`，只有 base.go 的 init 执行；
4. GC 后上述值不变；
5. 退出码 0，最后一行 `ALL PASS l15_tag_excluded_unparsed`。

## 失败判读

- `BUILD-FAIL` 且日志含 `no/such/package`：编译器解析了被排除文件的 import；
- `BUILD-FAIL` 且日志含 `syntax error`：排除前就完整解析了包体；
- `found packages main and otherpkg`：包名一致性检查未先应用构建约束；
- `redeclared`：被排除文件参与了重复符号检查；
- `baseInits!=1`：运行期仍链接了被排除文件的 init；
- 这类失败优先判为构建标签语义 bug，而非用例问题。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_excluded_unparsed
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
