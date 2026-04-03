package capabilities

import (
	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
)

type Capabilities struct {
	AddCapabilities  wrapper.StringFlags
	DropCapabilities wrapper.StringFlags
	Privileged       bool
}

type Name string
