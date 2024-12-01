package config

import (
	"encoding/json"
	"os"
	"path"
	"strings"
	"time"
)

type ALocFor uint8

const (
	ALocForHost ALocFor = iota
	ALocForHostPod
	ALocForPod
)

type NetIface struct {
	Name    string
	Type    string
	IP      string
	ALocFor ALocFor // host, host-pod (aka veth), pod
	Main    []NetIface
}

type StringFlags []string

func (f *StringFlags) String() string {
	b, _ := json.Marshal(*f)
	return string(b)
}

func (f *StringFlags) Set(value string) error {
	for _, str := range strings.Split(value, ",") {
		*f = append(*f, str)
	}
	return nil
}

type StringWrapper struct {
	Value string
}

type SquashFile struct {
	File   string
	LoopNo int
}

type Config struct {
	Name string

	Created int64
	HostPid int
	ContPid int
	TmpSize uint

	ProjectDir string

	Workdir   string
	Upperdir  string
	RootfsDir string

	ReadOnly   bool
	Remove     bool
	EnvAll     bool
	Background bool
	Startup    bool
	NS         map[string]*StringWrapper

	Exec        string
	Devtmpfs    string
	Resolv      string
	Hosts       string
	Status      string
	Dir         string
	Volumes     StringFlags
	SquashFiles []*SquashFile
	HostArgs    []string
	PodArgs     []string
	Lower       StringFlags
	RunPreExec  StringFlags
	RunPrePivot StringFlags
	PassEnv     StringFlags

	Ifaces []NetIface
}

var (
	TypeString string
	TypeInt    int
	TypeUint   uint
)

var Namespaces []string = []string{"pid", "net", "user", "uts", "ipc", "cgroup", "mnt", "time", "ns"}

func NewContainer() Config {
	Config := Config{}
	Config.HostPid = os.Getpid()
	Config.Created = time.Now().UTC().Unix()
	Config.Ifaces = []NetIface{{ALocFor: ALocForHost}}
	Config.NS = make(map[string]*StringWrapper, len(Namespaces))
	for _, ns := range Namespaces {
		Config.NS[ns] = &StringWrapper{Value: ""}
	}
	return Config
}

func (c Config) Json() []byte {
	conf, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return conf
}

var (
	// Main folder for all container related files
	LibDir string
	RunDir string

	BaseImageDir string
	BaseStateDir string

	BaseUpperdir         string
	BaseWorkdir          string
	BaseSquashFSMountDir string
	BaseRootfsDir        string

	DefaultHostNet string
)

func init() {

	LibDir = GetEnv("SANDAL_LIB_DIR", "/var/lib/sandal")
	RunDir = GetEnv("SANDAL_RUN_DIR", "/var/run/sandal")

	BaseImageDir = GetEnv("SANDAL_IMAGE_DIR", path.Join(LibDir, "image"))
	BaseStateDir = GetEnv("SANDAL_STATE_DIR", path.Join(LibDir, "state"))
	BaseUpperdir = GetEnv("SANDAL_UPPERDIR", path.Join(LibDir, "upper"))

	BaseWorkdir = GetEnv("SANDAL_WORKDIR", path.Join(RunDir, "workdir"))
	BaseRootfsDir = GetEnv("SANDAL_ROOTFSDIR", path.Join(RunDir, "rootfs"))
	BaseSquashFSMountDir = GetEnv("SANDAL_SQUASHFSMOUNTDIR", path.Join(RunDir, "squashfs"))

	DefaultHostNet = GetEnv("SANDAL_HOST_NET", "172.16.0.1/24;fd34:0135:0123::1/64")
}

type DefaultInformation struct {
	UpperDir  string
	Workdir   string
	RootFsDir string
}

func Defs(containerName string) DefaultInformation {
	return DefaultInformation{
		UpperDir:  path.Join(BaseUpperdir, containerName),
		Workdir:   path.Join(BaseWorkdir, containerName),
		RootFsDir: path.Join(BaseRootfsDir, containerName),
	}
}
