package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/capabilities"
	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/forward"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"github.com/ahmetozer/sandal/pkg/env"
)

// Allocate For a Network Interface {host: bridge interfaces such as sandal0 , host-pod: veth, pod: lo0}

type Config struct {
	Name string

	Created int64
	HostPid int
	ContPid int
	TmpSize       uint
	ChangeDirSize string // Change dir disk image size (e.g. "128m", "1g", default "128m")
	ChangeDirType string // "auto", "folder", "image"

	ChangeDir string
	RootfsDir string
	Snapshot  string

	// ChangeDirManaged signals that the caller (typically `sandal build`)
	// owns the change-dir backing across multiple host.RunContainer
	// invocations. When true, host.mountRootfs assumes the change dir is
	// already mounted and host.UmountRootfs leaves the change-dir backing
	// in place at container teardown. This lets build accumulate state in
	// the upper directory across successive RUN steps without losing data
	// to the unmount-remount cycle host normally performs per run.
	ChangeDirManaged bool

	ReadOnly        bool
	Remove          bool
	EnvAll          bool
	Background      bool
	Startup         bool
	TTY             bool
	NS              namespace.Namespaces
	Capabilities    capabilities.Capabilities
	User            string
	Devtmpfs        string
	Resolv          string
	Hosts           string
	Status          string
	Dir             string
	Volumes         wrapper.StringFlags
	ImmutableImages diskimage.ImmutableImages
	HostArgs        []string
	ContArgs        []string
	Lower           wrapper.StringFlags
	RunPreExec      wrapper.StringFlags
	RunPrePivot     wrapper.StringFlags
	PassEnv         wrapper.StringFlags
	Net             any
	Ports           []forward.PortMapping

	// VM execution context (empty string means no VM)
	VM string // "" = no VM, "kvm" = KVM, "vz" = VZ

	// Resource limits (cgroups v2)
	MemoryLimit string // Memory limit with units (e.g., "512M", "1G")
	CPULimit    string // CPU limit as number of CPUs (e.g., "0.5", "2")

	// CLI entrypoint override (like docker --entrypoint)
	Entrypoint string // Overrides image ENTRYPOINT when set
}

var (
	TypeString string
	TypeInt    int
	TypeUint   uint
)

func NewContainer() Config {
	Config := Config{}
	Config.HostPid = os.Getpid()
	Config.Created = time.Now().UTC().Unix()
	return Config
}

func (c Config) Json() []byte {
	conf, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	json.Indent(&buf, conf, "", "\t")
	return buf.Bytes()
}

type DefaultInformation struct {
	ChangeDir string
	RootFsDir string
}

func Defs(containerName string) DefaultInformation {
	return DefaultInformation{
		ChangeDir: path.Join(env.BaseChangeDir, containerName),
		RootFsDir: path.Join(env.BaseRootfsDir, containerName),
	}
}
