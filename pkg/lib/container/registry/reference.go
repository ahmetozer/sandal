package registry

import (
	"fmt"
	"strings"
)

// Reference holds parsed components of an image reference.
type Reference struct {
	Registry   string // API registry host, e.g. "registry-1.docker.io"
	Repository string // e.g. "library/alpine"
	Tag        string // e.g. "latest"
	Digest     string // e.g. "sha256:abc..."
}

// Ref returns the tag or digest to use in API calls.
func (r Reference) Ref() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

func (r Reference) String() string {
	s := r.Registry + "/" + r.Repository
	if r.Digest != "" {
		s += "@" + r.Digest
	} else if r.Tag != "" {
		s += ":" + r.Tag
	}
	return s
}

// ParseReference parses an image reference string.
// Examples:
//
//	busybox:latest                    -> registry-1.docker.io/library/busybox:latest
//	ubuntu                            -> registry-1.docker.io/library/ubuntu:latest
//	myregistry.com/repo:tag           -> myregistry.com/repo:tag
//	ghcr.io/owner/repo@sha256:abc...  -> ghcr.io/owner/repo@sha256:abc...
func ParseReference(raw string) (Reference, error) {
	var ref Reference

	// Split off digest.
	if idx := strings.Index(raw, "@"); idx != -1 {
		ref.Digest = raw[idx+1:]
		raw = raw[:idx]
	}

	// Split registry from repository:tag.
	// A registry host contains a dot or a colon (port), or is "localhost".
	var registry, remainder string
	slashIdx := strings.IndexByte(raw, '/')
	if slashIdx == -1 {
		// No slash: just an image name like "busybox" or "busybox:latest".
		registry = ""
		remainder = raw
	} else {
		firstPart := raw[:slashIdx]
		if strings.ContainsAny(firstPart, ".:") || firstPart == "localhost" {
			registry = firstPart
			remainder = raw[slashIdx+1:]
		} else {
			// e.g. "library/alpine" -> Docker Hub.
			registry = ""
			remainder = raw
		}
	}

	// Split tag from repository.
	// Be careful: digest refs already stripped above.
	if ref.Digest == "" {
		// Look for tag separator. Only split on the last colon that is
		// after the last slash (to avoid splitting port numbers which
		// were already handled as part of registry).
		lastSlash := strings.LastIndexByte(remainder, '/')
		colonIdx := strings.LastIndexByte(remainder, ':')
		if colonIdx > lastSlash {
			ref.Tag = remainder[colonIdx+1:]
			remainder = remainder[:colonIdx]
		}
	}

	ref.Repository = remainder

	// Normalize Docker Hub.
	if registry == "" || registry == "docker.io" || registry == "index.docker.io" {
		registry = "registry-1.docker.io"
		if !strings.Contains(ref.Repository, "/") {
			ref.Repository = "library/" + ref.Repository
		}
	}

	ref.Registry = registry

	// Default tag.
	if ref.Tag == "" && ref.Digest == "" {
		ref.Tag = "latest"
	}

	if ref.Repository == "" {
		return ref, fmt.Errorf("empty repository in reference %q", raw)
	}

	return ref, nil
}
