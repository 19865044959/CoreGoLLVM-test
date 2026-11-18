package main

// Fourth 声明在 Fifth 之后，但依赖 Fifth，必须先初始化 Fifth。
var (
	Fifth  = note("fifth", Third+5)
	Fourth = note("fourth", Fifth+5)
)
