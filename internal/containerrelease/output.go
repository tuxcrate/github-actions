package containerrelease

import (
	"errors"
	"os"
)

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(text)
	return errors.Join(writeErr, f.Close())
}

func (a app) output(name, value string) error {
	// Only validated metadata is written here, never arbitrary commit messages.
	return appendFile(a.getenv("GITHUB_OUTPUT"), name+"<<TUXCRATE_OUTPUT\n"+value+"\nTUXCRATE_OUTPUT\n")
}
