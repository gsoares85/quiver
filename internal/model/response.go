package model

import "time"

// Duration is a wall-clock duration. It currently serializes as an integer count of
// nanoseconds; a human-readable on-disk encoding is added by the storage layer.
type Duration time.Duration

// Response is the outcome of executing a request. It is produced at run time and is
// only persisted when captured as an Example.
type Response struct {
	Status     int          `json:"status"`
	StatusText string       `json:"statusText"`
	Headers    []Header     `json:"headers"`
	Body       []byte       `json:"body"`
	Size       int64        `json:"size"`
	Duration   Duration     `json:"duration"`
	Timing     Timing       `json:"timing"`
	Assertions []TestResult `json:"assertions,omitempty"`
}

// Timing captures the per-phase durations of a request's lifecycle.
type Timing struct {
	DNS      Duration `json:"dns"`
	Connect  Duration `json:"connect"`
	TLS      Duration `json:"tls"`
	TTFB     Duration `json:"ttfb"`
	Download Duration `json:"download"`
}

// TestResult is one assertion produced by a test script.
type TestResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}
