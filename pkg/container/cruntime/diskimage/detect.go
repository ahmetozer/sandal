package diskimage

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/tools/img"
	"github.com/ahmetozer/sandal/pkg/tools/sqfs"
)

func (i *ImmutableImage) detect() (immutableImageType, interface{}, error) {
	var (
		h   interface{}
		err error
	)
	//! Order is IMPORTANT othervise it can be false detect sqfs
	h, err = img.GetImageInfo(i.File)
	if err == nil {
		return ImmutableImageTypeImgMBR, h, nil
	}

	h, err = sqfs.Info(i.File)
	if err == nil {
		return ImmutableImageTypeSquashfs, h, nil
	}

	return 0, nil, fmt.Errorf("not supported image type %s", i.File)
}
