---
id: l06_iface_ptr_holder
dim: L06_reflect_iface
priority: P0
sources:
  - caddy@v2.10.0: context_test.go:22 装载模块后 mod.(io.Writer) 接口持有具体指针
  - fiber@v2.52.6: router_test.go:306 *App/*Group 装箱为 interface 后断言 Router
  - gorm@v1.30.0: schema/schema_test.go:38 全指针字段模型的反射复用
tags: [L06, interface, pointer-receiver, interface-data, pointer-to-interface, typed-nil, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

`Counter` 持 `*Payload`（内含嵌套 `*Node` 与 `*[4]*Node`），仅以接口形式返回并丢弃所有类型化根，
GC 后经接口断言、接口值拷贝、结构体接口字段、`*incer`、`any` 往返与 typed nil 接收者访问其指针
字段；再覆盖接口持有 `*[2]*Node` 与 `*[]*Node` 的场景。验证接口数据字中的指针在 GC 中始终被扫描。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_iface_ptr_holder`；
2. 接口是唯一根时，GC 后 `Counter`、`Payload`、`Node`、指针数组全部可解引用；
3. 接口值拷贝共享同一对象，经 incer/getter/结构体字段修改互相可见；
4. `any` 往返与 `*incer` 解引用在 GC 后保持动态类型和数据；
5. typed nil `*Counter` 装箱后接口非 nil，nil 接收者方法返回哨兵值且不崩溃；
6. nil 接口装箱得到 nil 接口；重复 GC 后所有接口槽与嵌套指针不变。

## 失败判读

- `payload survives via interface` / `nested tag pointer after GC`：接口数据区指针未被扫描（漏标）；
- `same object via original interface`：接口拷贝被错误实现为深拷贝，属语义错误；
- `*interface survives GC`：指向接口变量的指针位图错误；
- `nil receiver method via interface` / SIGSEGV：typed nil 判定或方法派发错误。

## 参数

- `UAF_SEED`：基础计数值与标签值来源；失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_ptr_holder/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
