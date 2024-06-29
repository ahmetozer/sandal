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

type Config struct {
	Name string

	Created   int64
	HostPid   int
	ContPid   int
	LoopDevNo int
	TmpSize   uint

	SquashfsFile string
	RootfsDir    string
	ReadOnly     bool
	Remove       bool
	EnvAll       bool
	Background   bool
	Startup      bool
	NS           map[string]*StringWrapper
	ChangeDir    string
	Exec         string
	Devtmpfs     string
	Resolv       string
	Hosts        string
	Status       string
	Volumes      StringFlags
	HostArgs     []string
	PodArgs      []string
	LowerDirs    StringFlags
	RunPreExec   StringFlags
	RunPrePivot  StringFlags

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
	Workdir    string = "/var/lib/sandal"
	Containers string = ""
)

func init() {

	if os.Getenv("SANDAL_WORKDIR") != "" {
		Workdir = os.Getenv("SANDAL_WORKDIR")
	}
	Containers = path.Join(Workdir, "containers")
}
