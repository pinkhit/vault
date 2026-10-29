//go:build !linux && !darwin

package storage

func (s *Store) VolumeCapacity() (Capacity, error) {
	return Capacity{}, ErrCapacity
}
