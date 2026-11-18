package main

// cHelper has its own defer stack; it must unwind before control returns to
// init C. init C then recovers a non-string panic value.
func cHelper() {
	defer func() { trace = append(trace, "c:h3") }()
	defer func() { trace = append(trace, "c:h2") }()
	trace = append(trace, "c:h1")
}

func init() {
	trace = append(trace, "c:enter")
	defer func() { trace = append(trace, "c:defer") }()
	cHelper()
	func() {
		defer func() {
			if r := recover(); r != nil {
				if n, ok := r.(int); ok && n == 42 {
					cRecovered++
				}
				trace = append(trace, "c:recover")
			}
		}()
		trace = append(trace, "c:panic-pre")
		panic(42)
	}()
	cCompleted = true
}
