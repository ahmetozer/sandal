package image

// Config holds OCI image runtime configuration defaults.
// These are extracted from the image manifest during pull and
// applied at container startup when CLI flags don't override them.
type Config struct {
	Env        []string // Image ENV (e.g., PATH=/usr/local/bin:/usr/bin)
	Entrypoint []string // Image ENTRYPOINT
	Cmd        []string // Image CMD
	WorkDir    string   // Image WORKDIR
	User       string   // Image USER
}
