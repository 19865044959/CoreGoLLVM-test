// Package base is the common dependency of mid, left, right and top.
package base

import "l12_module_init_order/sink"

// Base is set by a package-level variable initializer, before base.init runs.
var Base = 10

// BaseMark proves that package-level variable initializers run before init.
var BaseMark = sink.Add("base.var")

// BaseReady is set in init and folds in the initializer results.
var BaseReady bool

func init() {
	sink.Add("base.init")
	BaseReady = Base == 10 && BaseMark == "base.var"
}
