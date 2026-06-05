package configuration

// Build information embedded at link time via -ldflags. The defaults are
// used for unversioned local development builds.
var (
	BuildVersion   = "v0.0.0-dev"
	BuildCommit    = "none"
	BuildDate      = "unknown"
	BuildGoVersion = "unknown"
	BuildPlatform  = "unknown"
)
