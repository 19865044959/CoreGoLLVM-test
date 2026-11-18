package main

import "fmt"

// 兜底 init：补齐加载失败后缺失的默认项，并把可选模块的 panic 收敛为错误。
func init() {
	initSeq = append(initSeq, "fallback")

	defaults := []fixtureEntry{{"beta", 2}, {"gamma", 3}}
	for _, d := range defaults {
		if _, ok := globalMods.get(d.name); !ok {
			if err := globalMods.put(d.name, d.value); err != nil {
				modInitErrs["fallback"] = err
			}
		}
	}

	if err := safeLoadOptional(); err != nil {
		modInitErrs["optional"] = err
	}
}

// safeLoadOptional 用 recover 把可选模块的 panic 转成错误；
// 无论它与其他文件 init 的先后顺序如何，主流程都不 panic。
func safeLoadOptional() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered: %v", r)
		}
	}()
	panic("optional module broken")
}
