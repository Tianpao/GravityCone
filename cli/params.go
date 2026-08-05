//go:build !et_ffi

package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// --- Request parameter helpers ---

func (r *Request) getString(key string) (string, error) {
	v, ok := r.Params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", key)
	}
	return s, nil
}

// toInt converts a JSON parameter value to int. Accepts numbers and
// numeric strings (e.g. "25565"), since launchers commonly emit integer
// params as strings.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func (r *Request) getInt(key string) (int, error) {
	v, ok := r.Params[key]
	if !ok {
		return 0, fmt.Errorf("missing required parameter: %s", key)
	}
	n, ok := toInt(v)
	if !ok {
		return 0, fmt.Errorf("parameter %s must be a number", key)
	}
	return n, nil
}
