package main

// init2Seen verifies that the second init function also observes the fully
// initialized registration table. Cross-file init order is not asserted.
var init2Seen int

func init() {
	init2Seen = len(registry)
}
