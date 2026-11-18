package main

// 启动期内置注册：模拟包初始化阶段向全局注册表登记默认实现。
var (
	defaultRegistry = newRegistry()
	builtinInitErr  error
)

func init() {
	builtins := []struct {
		name  string
		value int
	}{
		{"gob", 1},
		{"json", 2},
		{"text", 3},
	}
	for _, b := range builtins {
		if err := defaultRegistry.Register(b.name, b.value); err != nil && builtinInitErr == nil {
			builtinInitErr = err
		}
	}
}
