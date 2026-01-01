package main

import (
	"fmt"
	"os"
)

func format(files []string) {
	// TODO: finish implementing this
	fmt.Fprintf(os.Stderr, "formatting %v\n", files)
	os.Exit(1)
}
