//go:build !windows

package main

import "errors"

// runElevated is only needed on Windows (Rose's folder in Program Files).
func runElevated(args []string) error {
	return errors.New("run the companion with administrator rights")
}
