package version

var version = "0.1.0-dev"

var name = "haustorium" //nolint:gochecknoglobals // Set via ldflags at build time.

// Version returns the compile time version.
func Version() string {
	return version
}

// Name returns the compile time name.
func Name() string {
	return name
}
