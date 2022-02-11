package protocol

import (
	"time"
)

type SpecialError int

const (
	SpecialErrorDEADBEAT SpecialError = 0xDEADBEA7
	SpecialErrorDEADGHOST SpecialError = 0xDEAD9057
)

type Report struct {
	WorkerName string
	Runs []RunResults
}

type RunResults struct {
	Scriptfile string
	Stdout []string
	Stderr []string
	ExitCode int
	StartTime time.Time
	EndTime time.Time
}

