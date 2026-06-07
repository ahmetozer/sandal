//go:build !linux && !darwin

package runtime

func isPidAlive(pid int) (bool, error) {
	return true, nil
}

func processStartTime(pid int) (uint64, error) {
	return 0, nil
}
