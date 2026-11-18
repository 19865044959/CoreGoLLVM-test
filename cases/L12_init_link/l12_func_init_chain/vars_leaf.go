package main

// b depends on a, so a must be initialized first regardless of file order.
var a = mkInt("a", 3)
var b = add(a, mkInt("b", 4))
