// Package env loads key=value pairs from a .env file into the process
// environment.
package env

import (
	"bufio"
	"os"
	"strings"
)

// Load reads the file at path and sets each key=value line as an
// environment variable. Blank lines and lines starting with # are skipped.
func Load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return scanner.Err()

}
