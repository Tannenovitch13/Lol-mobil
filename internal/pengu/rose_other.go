//go:build !windows

package pengu

// Rose is Windows-only.
func roseInstallDirs() []string { return nil }
