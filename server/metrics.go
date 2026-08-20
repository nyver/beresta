package server

import (
	"fmt"
	"io"
	"sync/atomic"
)

type Metrics struct {
	requests atomic.Uint64
	errors   atomic.Uint64
	activeWS atomic.Int64
}

func (m *Metrics) WritePrometheus(writer io.Writer) error {
	_, err := fmt.Fprintf(writer,
		"# TYPE beresta_http_requests_total counter\nberesta_http_requests_total %d\n"+
			"# TYPE beresta_http_errors_total counter\nberesta_http_errors_total %d\n"+
			"# TYPE beresta_websocket_connections gauge\nberesta_websocket_connections %d\n",
		m.requests.Load(), m.errors.Load(), m.activeWS.Load())
	return err
}
