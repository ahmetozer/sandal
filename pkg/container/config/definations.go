package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/namespace"
	"github.com/ahmetozer/sandal/pkg/env"
)

// Allocate For a Network Interface {host: bridge interfaces such as sandal0 , host-pod: veth, pod: lo0}

type Config struct {
	Name string

	Created int64
	HostPid int
	ContPid int
	TmpSize uint

	ChangeDir string
	RootfsDir string

	ReadOnly        bool
	Remove          bool
	EnvAll          bool
	Background      bool
	Startup         bool
	NS              namespace.Namespaces
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
