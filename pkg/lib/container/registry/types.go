package registry

// Media type constants for OCI and Docker image specs.
const (
	MediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeOCIIndex           = "application/vnd.oci.image.index.v1+json"
	MediaTypeOCIManifest        = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeDockerLayer        = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	MediaTypeOCILayer           = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeDockerConfig       = "application/vnd.docker.container.image.v1+json"
	MediaTypeOCIConfig          = "application/vnd.oci.image.config.v1+json"
)

// Platform identifies an OS/architecture combination.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// Descriptor references a content-addressable blob.
type Descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *Platform `json:"platform,omitempty"`
}

// Index (manifest list) points to platform-specific manifests.
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Manifests     []Descriptor `json:"manifests"`
}

// Manifest describes a single image: config + ordered layers.
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// ImageConfig is the OCI image configuration JSON.
type ImageConfig struct {
	Architecture string        `json:"architecture,omitempty"`
	OS           string        `json:"os,omitempty"`
	Config       RuntimeConfig `json:"config,omitempty"`
	RootFS       RootFS        `json:"rootfs"`
	History      []History     `json:"history,omitempty"`
}

// RuntimeConfig holds the container runtime settings.
type RuntimeConfig struct {
	Env          []string            `json:"Env,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Cmd          []string            `json:"Cmd,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	User         string              `json:"User,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Volumes      map[string]struct{} `json:"Volumes,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	StopSignal   string              `json:"StopSignal,omitempty"`
}

// RootFS describes the image's filesystem layers.
type RootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids"`
}

// History describes the history of a layer.
type History struct {
	CreatedBy  string `json:"created_by,omitempty"`
	Comment    string `json:"comment,omitempty"`
	EmptyLayer bool   `json:"empty_layer,omitempty"`
}
