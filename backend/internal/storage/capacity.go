package storage

import (
	"errors"
	"math"
)

var ErrCapacity = errors.New("filesystem capacity is unavailable")

// Capacity describes the entire filesystem containing objects, not allocated
// object bytes. Reserved free blocks are not available to the service account.
type Capacity struct {
	TotalBytes     int64 `json:"total_bytes"`
	UsedBytes      int64 `json:"used_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
	ReservedBytes  int64 `json:"reserved_bytes"`
	LowSpace       bool  `json:"low_space"`
}

func capacity(blockSize, blocks, free, available uint64) (Capacity, error) {
	if blockSize == 0 || blocks == 0 || free > blocks || available > free || blocks > math.MaxInt64/blockSize {
		return Capacity{}, ErrCapacity
	}
	result := Capacity{
		TotalBytes:     int64(blocks * blockSize),
		UsedBytes:      int64((blocks - free) * blockSize),
		AvailableBytes: int64(available * blockSize),
		ReservedBytes:  int64((free - available) * blockSize),
	}
	result.LowSpace = result.AvailableBytes < 1<<30 || float64(available)/float64(blocks) < 0.10
	return result, nil
}
