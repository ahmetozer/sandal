package config

import (
	"encoding/json"
	"os"
	"path"
	"strings"
	"time"
)

type NS struct {
	Net  string
	Pid  string
	Uts  string
	User string
}

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

type Volumes []string

func (f *Volumes) String() string {
	b, _ := json.Marshal(*f)
	return string(b)
}

func (f *Volumes) Set(value string) error {
	for _, str := range strings.Split(value, ",") {
		*f = append(*f, str)
	}
	return nil
}

type Config struct {
	Name string

	Created   int64
	HostPid   int
	PodPid    int
	LoopDevNo int
	TmpSize   uint

	SquashfsFile string
	RootfsDir    string
	ReadOnly     bool
	RemoveOnExit bool
	EnvAll       bool
	NS           NS
	ChangeDir    string
	Exec         string
	Devtmpfs     string
	Resolv       string
	Hosts        string
	Volumes      Volumes

	Ifaces []NetIface
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
	Config.Ifaces = []NetIface{{ALocFor: ALocForHost}}
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
	Containers = path.Join(Workdir, "containers")
}
