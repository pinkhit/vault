package storage

import "golang.org/x/sys/unix"

func capacityBlockSize(stat unix.Statfs_t) uint64 {
	if stat.Bsize > 0 {
		return uint64(stat.Bsize)
	}
	return 0
}
