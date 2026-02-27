package handlers

import (
	"fmt"
	"strconv"
)

// parseUintParam parses a string into a uint.
func parseUintParam(param string) (uint, error) {
	parsed, err := strconv.ParseUint(param, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed > uint64(^uint(0)) {
		return 0, fmt.Errorf("value out of range for uint: %d", parsed)
	}
	return uint(parsed), nil
}
