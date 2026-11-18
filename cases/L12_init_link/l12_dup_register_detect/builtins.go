package main

func init() {
	if _, err := plugins.add("json", 2); err != nil {
		panic(err)
	}
	if _, err := plugins.add("gob", 3); err != nil {
		panic(err)
	}
}
