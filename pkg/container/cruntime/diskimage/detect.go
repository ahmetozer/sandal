package diskimage

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/tools/img"
	"github.com/ahmetozer/sandal/pkg/tools/sqfs"
)

func (i *ImmutableImage) detect() (immutableImageType, interface{}, error) {
	var (
		h     interface{}
		err   error
		errs  []error
		errno uint8
	)
	//! Order is IMPORTANT othervise it can be false detect sqfs
	h, err = img.GetImageInfo(i.File)
	if err == nil {
		return ImmutableImageTypeImgMBR, h, nil
	} else {
		errno += 1
		errs = append(errs, fmt.Errorf("%d: %s", errno, err))
	}

	h, err = sqfs.Info(i.File)
	if err == nil {
		return ImmutableImageTypeSquashfs, h, nil
	} else {
		errno += 1
		errs = append(errs, fmt.Errorf("%d: %s", errno, err))
	}

	return 0, nil, fmt.Errorf("not supported image type %s %v", i.File, errs)
}
