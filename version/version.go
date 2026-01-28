package version

//nolint:gochecknoglobals // Set via ldflags at build time.
var (
	version = "0.1.0-dev"
	name    = "haustorium"
	commit  = "undefined"
)

// Commit returns the compile time commit.
func Commit() string {
	return commit
}

// Version returns the compile time version.
func Version() string {
	return version
}

// Name returns the compile time name.
func Name() string {
	return name
}
