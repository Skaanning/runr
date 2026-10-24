// Command runr runs multiple local projects at once and shows their output in a TUI.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "runr:", err)
		os.Exit(1)
	}
}
