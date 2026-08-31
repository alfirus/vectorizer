package store

import "sync/atomic"

// Metrics tracks real counters for the /metrics endpoint.
type Metrics struct {
	MessagesAdded  atomic.Int64
	SearchesTotal  atomic.Int64
	SearchesFailed atomic.Int64
}

var GlobalMetrics = &Metrics{}
