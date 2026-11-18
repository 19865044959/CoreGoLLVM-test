package main

// init gamma validates a correct fixture and records no error, proving that
// aggregate skips nil slots.
func init() {
	initMarks = append(initMarks, "gamma")
	if err := validateFixture(validFixture()); err != nil {
		slots[2] = err
	}
}
