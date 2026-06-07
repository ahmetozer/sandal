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

	// HostPidStart / ContPidStart are the kernel start-times
	// (/proc/<pid>/stat field 22) of HostPid / ContPid, captured right after
	// the process is spawned. Paired with the pid they form an identity that
	// survives a daemon restart and defeats PID reuse: a recycled pid has a
	// different start-time, so liveness/kill checks won't act on an unrelated
	// process. 0 means "unknown" — checks degrade to plain liveness.
	HostPidStart uint64
	ContPidStart uint64

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

// MonitorPidIdentity returns the pid to watch for liveness and its expected
// start-time. VM containers are tracked by HostPid (the KVM process; their
// host-side ContPid is never set); native containers by ContPid. Pair the
// returned pid with the start-time via runtime.IsPidRunningAs so a recycled pid
// is not mistaken for the container.
func (c *Config) MonitorPidIdentity() (pid int, startTime uint64) {
	if c.VM != "" {
		return c.HostPid, c.HostPidStart
	}
	return c.ContPid, c.ContPidStart
}

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

// Clone returns a deep copy of c. All reference-typed fields (the NS map,
// Capabilities' StringFlags slices, Volumes/Lower/Run*/PassEnv, HostArgs,
// ContArgs, ImmutableImages, Net, Ports) are duplicated so mutating the
// clone never affects the original. Implementation round-trips through
// JSON: the same encoding controller.LoadFile and net.ToLinks already
// rely on, so type behavior of Net (an `any`) matches the disk path.
func (c *Config) Clone() *Config {
	data, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	var out Config
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
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
