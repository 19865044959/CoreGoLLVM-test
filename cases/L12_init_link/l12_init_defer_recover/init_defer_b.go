package main

// init B recovers a panic inside a nested function; the enclosing init must
// continue afterwards and still run its own defer.
func init() {
	trace = append(trace, "b:enter")
	func() {
		defer func() {
			if r := recover(); r != nil {
				if s, ok := r.(string); ok && s == "b-boom" {
					bRecovered++
				}
				trace = append(trace, "b:recover")
			}
		}()
		trace = append(trace, "b:panic-pre")
		panic("b-boom")
	}()
	trace = append(trace, "b:after")
	defer func() { trace = append(trace, "b:defer") }()
}
