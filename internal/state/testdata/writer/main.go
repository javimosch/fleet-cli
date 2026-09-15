// writer is a test helper: a separate process that appends to one key.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/javimosch/fleet-cli/internal/state"
)

func main() {
	path, key := os.Args[1], os.Args[2]
	n, _ := strconv.Atoi(os.Args[3])
	for i := 0; i < n; i++ {
		s, err := state.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open:", err)
			os.Exit(1)
		}
		if err := s.Append(key, i); err != nil {
			fmt.Fprintln(os.Stderr, "append:", err)
			os.Exit(1)
		}
	}
}
