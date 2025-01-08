package config

import (
	"encoding/json"
	"os"
	"path"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

// Allocate For a Network Interface {host: bridge interfaces such as sandal0 , host-pod: veth, pod: lo0}
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

type Config struct {
	Name string

	Created int64
	HostPid int
	ContPid int
	TmpSize uint

	ProjectDir string

	ChangeDir string
	RootfsDir string

	ReadOnly   bool
	Remove     bool
	EnvAll     bool
	Background bool
	Startup    bool
	NS         map[string]*StringWrapper

	Devtmpfs        string
	Resolv          string
	Hosts           string
	Status          string
	Dir             string
	Volumes         StringFlags
	ImmutableImages []interface{}
	HostArgs        []string
	PodArgs         []string
	Lower           StringFlags
	RunPreExec      StringFlags
	RunPrePivot     StringFlags
	PassEnv         StringFlags

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
