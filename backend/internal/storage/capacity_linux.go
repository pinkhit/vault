package storage

import "golang.org/x/sys/unix"

func capacityBlockSize(stat unix.Statfs_t) uint64 {
	// Linux reports block counts in fragment units when supplied.
	if stat.Frsize > 0 {
		return uint64(stat.Frsize)
	}
	if stat.Frsize == 0 && stat.Bsize > 0 {
		return uint64(stat.Bsize)
	}
	return 0
}
