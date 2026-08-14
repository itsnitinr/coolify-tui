package config

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that reads and writes as a human string in YAML
// ("10s", "1m30s") rather than as a raw nanosecond count, so the config file
// stays comfortable to edit by hand.
type Duration time.Duration

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String renders the duration in Go's usual notation.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalYAML writes the duration as a string.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML accepts a duration string ("10s"), or a bare number which is
// interpreted as seconds.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("config: refresh interval must be a string like \"10s\": %w", err)
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q (want e.g. \"10s\" or \"1m\"): %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}
