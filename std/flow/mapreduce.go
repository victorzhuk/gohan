package flow

import (
	"context"

	gohan "github.com/victorzhuk/gohan/core"
)

// MapReduce returns a Flow over input slices that invokes child once per
// input under the wave's failure policy and concurrency cap, then calls
// reduce once with the results in input order. Results land at their input
// positions regardless of completion order; the wave shares one concurrency
// cap across every concurrent invocation of the returned flow.
func MapReduce[In, Mid, Out any](child gohan.Flow[In, Mid], reduce func([]Mid) (Out, error), opts ...FanOption) gohan.Flow[[]In, Out] {
	c := newFanConfig(opts)
	sem := make(chan struct{}, c.parallelism)
	return fanFlow[[]In, Out]{
		fn: func(ctx context.Context, in []In) (Out, error) {
			var out Out
			mids, err := runFan(ctx, sem, c.onError, in, func(ctx context.Context, _ int, v In) (Mid, error) {
				return child.Invoke(ctx, v)
			})
			if err != nil {
				return out, err
			}
			return reduce(mids)
		},
	}
}
