//go:build !windows

package config

import "os"

// permissionsEnforced reports whether the platform's file mode bits actually
// govern access to a file. They do on Unix, so the config's 0600 mode is a real
// protection and worth checking.
const permissionsEnforced = true

// modeIsLoose reports whether a mode grants any access beyond the owner.
func modeIsLoose(mode os.FileMode) bool {
	return mode&0o077 != 0
}
