//go:build ignore

package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	data, err := os.ReadFile("media/processor.go")
	if err != nil {
		panic(err)
	}
	lines := strings.Split(string(data), "\n")
	for _, i := range []int{278, 279, 297, 298, 340, 341, 342, 426, 427} {
		fmt.Printf("%d: %q\n", i, lines[i-1])
	}
}
