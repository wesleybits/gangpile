package main

import (
	"fmt"
	"os"

	"github.com/wesleybits/gangpile/worker"
)

func main() {
	name := os.Args[1]
	if peon, err := worker.NewUDSWorker(name); err != nil {
		fmt.Printf("Initialization error: %s", err.Error())
		os.Exit(1)
	} else {
		worker.RunRPC(peon)
	}
}
