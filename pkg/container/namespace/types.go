package namespace

type NamespaceConf struct {
	UserValue     *string
	IsUserDefined bool
	IsHost        bool
}

type Name string

type Namespaces map[Name]NamespaceConf
