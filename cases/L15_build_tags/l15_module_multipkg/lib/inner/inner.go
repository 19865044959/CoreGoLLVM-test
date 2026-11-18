package inner

const Bias int64 = 100

var ready = true

func Ready() bool { return ready }

func Scale(v int64) int64 { return v*3 + 1 }
