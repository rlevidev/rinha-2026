package main

import (
	"os"

	"github.com/rlevidev/rinha-2026/internal/lb"
)

func main() {
	lb.PickBackend(0, os.Args[1:])
}
