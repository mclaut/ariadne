//go:build windows

// Command install directs Windows users to the native PowerShell installer.
package main

import "fmt"

func main() {
	fmt.Println("Use install.ps1 to install Ariadne on Windows.")
}
