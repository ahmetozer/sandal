package build

import (
	"fmt"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
)

// Stage is a build stage (one FROM ... up to the next FROM or end).
type Stage struct {
	// Index is the stage's 0-based position in the Dockerfile.
	Index int
	// Name is the alias from "FROM x AS name", or "" if unnamed.
	Name string
	// BaseRef is the FROM argument after ARG expansion.
	BaseRef string
	// Instrs are the instructions in this stage in source order, NOT
	// including the FROM itself.
	Instrs []Instruction
	// Config is the OCI runtime config accumulated from ENV/CMD/etc.
	// Initially seeded from the base image's config.
	Config registry.RuntimeConfig
	// History records each instruction for OCI image history.
	History []registry.History
	// RootfsDir is the on-disk merged rootfs after the stage finishes;
	// retained so downstream stages can use COPY --from=<this>.
	RootfsDir string
}

// SplitStages groups a flat instruction list into stages by FROM boundaries.
// Each stage's Instrs excludes the FROM itself.
//
// ARG instructions appearing BEFORE the first FROM are "global ARGs" and
// are returned separately; they form the initial build-arg scope used to
// expand FROM references.
func SplitStages(instrs []Instruction) (globalArgs []Instruction, stages []*Stage, err error) {
	var current *Stage
	for _, in := range instrs {
		if in.Kind == InstrFrom {
			name, base, err := parseFromArgs(in.Args)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: %w", in.Line, err)
			}
			s := &Stage{
				Index:   len(stages),
				Name:    name,
				BaseRef: base,
			}
			stages = append(stages, s)
			current = s
			continue
		}
		if current == nil {
			// Pre-FROM: only ARG is allowed.
			if in.Kind != InstrArg {
				return nil, nil, fmt.Errorf("line %d: %s before any FROM is not allowed (only ARG)", in.Line, in.Kind)
			}
			globalArgs = append(globalArgs, in)
			continue
		}
		current.Instrs = append(current.Instrs, in)
	}
	if len(stages) == 0 {
		return nil, nil, fmt.Errorf("dockerfile contains no FROM instruction")
	}
	return globalArgs, stages, nil
}

// parseFromArgs parses a FROM instruction's argument list into (alias, baseRef).
// Accepted forms (post-flag-strip):
//
//	FROM image:tag
//	FROM image:tag AS alias
func parseFromArgs(args []string) (alias, base string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("FROM requires an image reference")
	}
	switch len(args) {
	case 1:
		return "", args[0], nil
	case 3:
		if !strings.EqualFold(args[1], "AS") {
			return "", "", fmt.Errorf("FROM: expected AS, got %q", args[1])
		}
		return args[2], args[0], nil
	default:
		return "", "", fmt.Errorf("FROM: invalid argument count %d", len(args))
	}
}

// FindStage returns the stage matching name (alias or stringified index),
// or nil if none. Used by COPY --from=<name>.
func FindStage(stages []*Stage, name string) *Stage {
	for _, s := range stages {
		if s.Name == name {
			return s
		}
		if fmt.Sprintf("%d", s.Index) == name {
			return s
		}
	}
	return nil
}
