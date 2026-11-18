package engine

import "l15_multipkg_tags/flags"

type Result struct {
	ID       int64
	Mode     string
	Value    int64
	Platform string
	Tag      string
}

func Run(id int64) *Result {
	s := flags.Default()
	return &Result{
		ID:       id,
		Mode:     s.Mode,
		Value:    id*3 + s.Scale,
		Platform: platformName(),
		Tag:      "engine",
	}
}
