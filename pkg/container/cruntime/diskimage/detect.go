package diskimage

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/lib/img"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func (i *ImmutableImage) detect() (immutableImageType, interface{}, error) {
	var (
		h     interface{}
		s     img.PartitionScheme
		err   error
		errs  []error
		errno uint8
	)
	//! Order is IMPORTANT othervise it can be false detect sqfs
	h, s, err = img.GetImageInfo(i.File)
	if err == nil {
		switch s {
		case img.PartitionMBR:
			return ImmutableImageTypeImgMBR, h, nil
		case img.PartitionGPT:
			return ImmutableImageTypeImgGPT, h, nil
		}
	} else {
		errno += 1
		errs = append(errs, fmt.Errorf("%d: %s", errno, err))
	}

	h, err = squashfs.Info(i.File)
	if err == nil {
		return ImmutableImageTypeSquashfs, h, nil
	} else {
		errno += 1
		errs = append(errs, fmt.Errorf("%d: %s", errno, err))
	}

	return 0, nil, fmt.Errorf("not supported image type %s %v", i.File, errs)
}
