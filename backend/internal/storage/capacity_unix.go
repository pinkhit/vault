//go:build linux || darwin

package storage

import "golang.org/x/sys/unix"

// VolumeCapacity measures a fixed, confined directory descriptor. Callers cannot
// select a mount, device or host path through an HTTP request.
func (s *Store) VolumeCapacity() (Capacity, error) {
	file, err := s.root.OpenFile("objects", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return Capacity{}, err
	}
	defer file.Close()
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return Capacity{}, err
	}
	return capacity(capacityBlockSize(stat), stat.Blocks, stat.Bfree, stat.Bavail)
}
