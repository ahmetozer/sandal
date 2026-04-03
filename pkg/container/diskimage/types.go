package diskimage

import (
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
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

type ImmutableImages []ImmutableImage

func (images ImmutableImages) Contains(image ImmutableImage) bool {
	for i := range images {
		if images[i].File == image.File {
			return true
		}
	}
	return false
}

func (images *ImmutableImages) ReplaceWith(image ImmutableImage) bool {
	for i := range *images {
		if (*images)[i].File == image.File {
			(*images)[i] = image
			return true
		}
	}
	return false
}
