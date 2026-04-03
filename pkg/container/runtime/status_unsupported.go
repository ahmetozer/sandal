//go:build !linux && !darwin

package runtime

func isPidAlive(pid int) (bool, error) {
	return true, nil
}
