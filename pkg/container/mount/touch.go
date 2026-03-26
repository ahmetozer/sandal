package mount

import (
	"os"
	"path/filepath"
)

// Touch creates an empty file at path if it does not already exist,
// creating parent directories as needed.
func Touch(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createTry := false

	CREATE_FILE:
		file, err := os.Create(path)
		if os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(path), 0o0600)
			if !createTry {
				createTry = true
				goto CREATE_FILE
			}
		} else if err != nil {
			return err
		}
		file.Close()
		return nil
	} else if err != nil {
		return err
	}
	return nil
}
