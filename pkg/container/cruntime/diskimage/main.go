package diskimage

import (
	"github.com/ahmetozer/sandal/pkg/tools/loopdev"
)

type immutableImageType uint8

const (
	ImmutableImageTypeUnknown immutableImageType = iota
	ImmutableImageTypeSquashfs
	ImmutableImageTypeImgMBR
	ImmutableImageTypeImgGPT
)

type ImmutableImage struct {
	File         string
	LoopConfig   loopdev.Config
	MountDir     string
	Type         immutableImageType
	mountOptions interface{}
	info         interface{}
	path         string
}

func (o immutableImageType) String() string {
	return map[immutableImageType]string{
		ImmutableImageTypeUnknown:  "unknown",
		ImmutableImageTypeSquashfs: "squashfs",
		ImmutableImageTypeImgMBR:   "mbr",
		ImmutableImageTypeImgGPT:   "gpt",
	}[o]
}
