package model

import "time"

// Duration is a wall-clock duration. It currently serializes as an integer count of
// nanoseconds; a human-readable on-disk encoding is added by the storage layer.
type Duration time.Duration

// Response is the outcome of executing a request. It is produced at run time and is
// only persisted when captured as an Example.
type Response struct {
	Status     int          `json:"status" yaml:"status"`
	StatusText string       `json:"statusText" yaml:"statusText"`
	Headers    []Header     `json:"headers" yaml:"headers"`
	Body       []byte       `json:"body" yaml:"body"`
	Size       int64        `json:"size" yaml:"size"`
	Duration   Duration     `json:"duration" yaml:"duration"`
	Timing     Timing       `json:"timing" yaml:"timing"`
	Assertions []TestResult `json:"assertions,omitempty" yaml:"assertions,omitempty"`
}

// Timing captures the per-phase durations of a request's lifecycle.
type Timing struct {
	DNS      Duration `json:"dns" yaml:"dns"`
	Connect  Duration `json:"connect" yaml:"connect"`
	TLS      Duration `json:"tls" yaml:"tls"`
	TTFB     Duration `json:"ttfb" yaml:"ttfb"`
	Download Duration `json:"download" yaml:"download"`
}

// TestResult is one assertion produced by a test script.
type TestResult struct {
	Name    string `json:"name" yaml:"name"`
	Passed  bool   `json:"passed" yaml:"passed"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
}
