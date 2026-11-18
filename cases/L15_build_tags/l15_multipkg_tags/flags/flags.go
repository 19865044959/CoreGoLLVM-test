package flags

type Settings struct {
	Mode  string
	Scale int64
}

func Default() Settings {
	return Settings{Mode: modeName(), Scale: baseScale}
}
