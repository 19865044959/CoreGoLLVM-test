package main

import "fmt"

type entry struct {
	Name  string
	Value int
}

func validFixture() []entry {
	return []entry{{Name: "alpha", Value: 1}, {Name: "beta", Value: 2}, {Name: "gamma", Value: 3}}
}

func badFixture() []entry {
	return []entry{{Name: "alpha", Value: 1}, {Name: "beta", Value: -5}, {Name: "gamma", Value: 3}}
}

func dupFixture() []entry {
	return []entry{{Name: "alpha", Value: 1}, {Name: "alpha", Value: 2}}
}

func emptyNameFixture() []entry {
	return []entry{{Name: "", Value: 1}}
}

// validateFixture returns the first classified error; error text and identity
// are deterministic for a fixed input.
func validateFixture(entries []entry) error {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if e.Name == "" {
			return fmt.Errorf("entry %d: %w", i, errEmptyKey)
		}
		if seen[e.Name] {
			return fmt.Errorf("entry %d %q: %w", i, e.Name, errDupKey)
		}
		if e.Value < 0 {
			return fmt.Errorf("entry %d %q: %w", i, e.Name, errNegative)
		}
		seen[e.Name] = true
	}
	return nil
}
