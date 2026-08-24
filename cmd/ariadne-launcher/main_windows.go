package main

import (
	"fmt"
	"os"
)

func main() {
	_, _ = fmt.Fprintln(os.Stderr, "Ariadne.app launcher is only available on macOS")
	os.Exit(1)
}
