package main

func (c circle) Name() string {
	if c.tag != "" {
		return c.tag
	}
	return "circle"
}
