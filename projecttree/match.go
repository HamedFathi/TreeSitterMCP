package projecttree

import (
	"errors"
	"path"
	"strings"
)

// Match supports basename globs and slash-separated path globs with ** segments.
// A pattern without a slash matches the basename at any depth.
func Match(pattern, name string) (bool, error) {
	if pattern == "" || len(pattern) > 4096 {
		return false, errors.New("glob must contain 1-4096 bytes")
	}
	patterns := strings.Split(pattern, "/")
	if len(patterns) > 64 {
		return false, errors.New("glob has too many segments")
	}
	for _, part := range patterns {
		if part != "**" {
			if _, err := path.Match(part, ""); err != nil {
				return false, err
			}
		}
	}
	if len(patterns) == 1 && pattern != "**" {
		return path.Match(pattern, path.Base(name))
	}
	parts := strings.Split(name, "/")
	previous := make([]bool, len(parts)+1)
	previous[0] = true
	for _, part := range patterns {
		next := make([]bool, len(previous))
		if part == "**" {
			next[0] = previous[0]
			for j := 1; j < len(next); j++ {
				next[j] = previous[j] || next[j-1]
			}
		} else {
			for j := 1; j < len(next); j++ {
				matched, _ := path.Match(part, parts[j-1])
				next[j] = previous[j-1] && matched
			}
		}
		previous = next
	}
	return previous[len(parts)], nil
}
