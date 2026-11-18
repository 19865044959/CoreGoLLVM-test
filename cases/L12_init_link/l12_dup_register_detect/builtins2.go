package main

func init() {
	if _, err := plugins.add("text", 1); err != nil {
		panic(err)
	}
}
