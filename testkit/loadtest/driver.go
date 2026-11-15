package loadtest

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Result is one run's outcome.
type Result struct {
	Index   int
	Started time.Time
	Done    time.Time
	Wall    time.Duration
	Err     error
}

var errNilFunc = errors.New("loadtest: nil run function")

// Run starts k concurrent runs that each call fn, and returns one Result per
// index in index order. All workers enter fn before any of them may proceed
// past the internal start barrier. k < 1 runs zero workers.
func Run(ctx context.Context, k int, fn func(ctx context.Context, i int) error) []Result {
	if k < 1 {
		return nil
	}
	results := make([]Result, k)
	var wg sync.WaitGroup
	entered := make(chan struct{}, k)
	release := make(chan struct{})
	for i := range k {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entered <- struct{}{}
			<-release
			start := time.Now()
			var err error
			if fn != nil {
				err = fn(ctx, i)
			} else {
				err = errNilFunc
			}
			done := time.Now()
			results[i] = Result{Index: i, Started: start, Done: done, Wall: done.Sub(start), Err: err}
		}(i)
	}
	for range k {
		<-entered
	}
	close(release)
	wg.Wait()
	return results
}
