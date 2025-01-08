package diskimage

import (
	"github.com/ahmetozer/sandal/pkg/container/cruntime/loop"
)

type immutableImageType uint8

const (
	ImmutableImageTypeSquashfs immutableImageType = iota + 1
	ImmutableImageTypeImgMBR
)

type ImmutableImage struct {
	File         string
	LoopConfig   loop.Config
	MountDir     string
	Type         immutableImageType
	mountOptions interface{}
	info         interface{}
	path         string
}

func (o immutableImageType) String() string {
	return map[immutableImageType]string{ImmutableImageTypeSquashfs: "squashfs", ImmutableImageTypeImgMBR: "mbr"}[o]
}
