package config

import (
	"fmt"
	"math/big"
	"math/rand"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

var validContainerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateName checks that a container name is safe for use in file paths
// and cgroup directories. It rejects empty names, names longer than 128
// characters, and names containing path separators or special characters.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("container name must not be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("container name must not exceed 128 characters")
	}
	if !validContainerName.MatchString(name) {
		return fmt.Errorf("container name %q is invalid: must match [a-zA-Z0-9][a-zA-Z0-9_.-]*", name)
	}
	return nil
}

func GenerateContainerId() string {
	time := big.NewInt(time.Now().UnixNano()).Text(62)
	r := big.NewInt(rand.Int63()).Text(62)
	return time + r
}

func (c *Config) ConfigFileLoc() string {
	return ConfigFileLoc(c.Name)
}

func ConfigFileLoc(name string) string {
	return path.Join(env.BaseStateDir, name+".json")
}

// NameFromConfigFile is the inverse of ConfigFileLoc for a state file's base
// name: "my.app.json" -> ("my.app", true). It strips the ".json" suffix rather
// than splitting on ".", so names containing dots (which ValidateName permits)
// survive. Returns ok=false when base is not a ".json" file or the decoded name
// is not a valid container name.
func NameFromConfigFile(base string) (string, bool) {
	name, ok := strings.CutSuffix(base, ".json")
	if !ok {
		return "", false
	}
	if ValidateName(name) != nil {
		return "", false
	}
	return name, true
}
