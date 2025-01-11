package diskimage

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/img"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
)

func (i *ImmutableImage) parseImagePath() error {

	u, err := url.Parse(strings.Replace(i.path, ":", "?", 1))
	if err != nil {
		return err
	}

	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return err
	}

	if q.Get("part") == "" {
		return fmt.Errorf("no partition is provided ex /my/disk.img:part=2")
	}

	part, err := strconv.ParseUint(q.Get("part"), 0, 8)
	if err != nil {
		return fmt.Errorf("unable to get partition info %s", err)
	}

	var offset uint64
	switch v := i.info.(type) {
	case ([]img.MBRPartitionEntry):
		if int(part) > len(v) {
			return fmt.Errorf("disk image has %d partition but you requested %d.th parth", len(v), part)
		}
		if part == 0 {
			return fmt.Errorf("partition cannot be zero, please set positive numbers")
		}

		offset = uint64(v[part-1].StartByte())
	case ([]img.GPTPartitionEntry):
		if int(part) > len(v) {
			return fmt.Errorf("disk image has %d partition but you requested %d.th parth", len(v), part)
		}
		if part == 0 {
			return fmt.Errorf("partition cannot be zero, please set positive numbers")
		}

		offset = v[part-1].StartByte()
	default:
		return fmt.Errorf("unknown ImmutableImage info header")
	}

	i.LoopConfig.Info = &loopdev.LoopInfo64{
		Offset: offset,
		Flags:  0,
	}

	return nil
}
