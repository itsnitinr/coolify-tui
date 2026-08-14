//go:build windows

package config

import "os"

// permissionsEnforced reports whether the platform's file mode bits actually
// govern access to a file.
//
// They do not on Windows. NTFS uses ACLs, and Go's os.Chmod only toggles the
// read-only attribute — os.Stat reports 0666 for any writable file regardless of
// who can actually read it. Checking the mode there would warn on every run,
// and would tell the user to run chmod, which does not exist on Windows.
//
// The config still lands under %LocalAppData%, whose default ACL is scoped to
// the user account, so the token is not world-readable. It is simply not the
// mode bits that make it so.
const permissionsEnforced = false

// modeIsLoose always reports false: see permissionsEnforced.
func modeIsLoose(os.FileMode) bool { return false }
