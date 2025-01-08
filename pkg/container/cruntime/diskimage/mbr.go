package diskimage

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ahmetozer/sandal/pkg/tools/img"
	"github.com/ahmetozer/sandal/pkg/tools/loopdev"
)

type MountDataMBR struct {
	PartitionNo uint8
	Offset      uint32
}

func (i *ImmutableImage) parseMbrPath() error {

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

	var offset uint32
	switch v := i.info.(type) {
	case img.Partitions:
		if int(part) > len(v) {
			return fmt.Errorf("disk image has %d partition but you requested %d.th parth", len(v), part)
		}
		if part == 0 {
			return fmt.Errorf("partition cannot be zero, please set positive numbers")
		}
		offset = 512 * v[part-1].Entry.(img.MBRPartitionEntry).StartLBA
	default:
		return fmt.Errorf("unkown ImmutableImage info header")
	}

	i.mountOptions = MountDataMBR{
		PartitionNo: uint8(part),
		Offset:      offset,
	}

	i.LoopConfig.Info = &loopdev.LoopInfo64{
		Offset: uint64(offset),
		Flags:  0,
	}

	return nil
}
