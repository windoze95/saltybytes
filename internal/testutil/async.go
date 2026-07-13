package testutil

import "time"

// AsyncDeadline bounds the test helpers that poll for a background goroutine to
// finish (video import jobs, multi-recipe resolution, hub room teardown).
//
// It is deliberately generous, and that costs nothing: every one of those loops
// exits the moment the work lands, so the happy path is unaffected. The deadline
// only decides how long a genuinely stuck test waits before failing.
//
// A tight bound here is really a timing assertion on the Go scheduler. Under a
// loaded parallel suite — `go test ./...` compiling and running every package at
// once, which is exactly what the CI deploy gate does — a background goroutine
// can be starved for seconds. That is how TestVideoImport_NativeVideoUsed failed
// at a 3-second deadline while passing in isolation: nothing was broken, the
// machine was just busy. Flaky gates get ignored, so buy the margin.
const AsyncDeadline = 30 * time.Second
