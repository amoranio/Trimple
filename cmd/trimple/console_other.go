//go:build !windows

package main

import (
	"fmt"
	"os"
)

func attachConsoleIfAny() {}

func showError(title, msg string) { fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg) }
