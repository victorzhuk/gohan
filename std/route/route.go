package route

import (
	"errors"

	"github.com/victorzhuk/gohan/core/types"
)

// Router orders a profile's endpoints and guards each with a breaker.
// Frozen: no spec declares a Router type. The strategy is chosen at
// construction: the static declaration order by default, or endpoints of
// one LatencyClass first with WithLatencyClass.
type Router struct {
	models   []types.Model
	class    types.LatencyClass
	classful bool
	breakers map[string]*Breaker
	pending  map[string]*Breaker
}

// RouterOption configures a Router.
type RouterOption func(*Router)

// WithLatencyClass puts endpoints of class first, keeping the declaration
// order inside each group.
func WithLatencyClass(class types.LatencyClass) RouterOption {
	return func(r *Router) {
		r.class = class
		r.classful = true
	}
}

// WithBreaker replaces the endpoint's default breaker with a tuned one.
func WithBreaker(name string, b *Breaker) RouterOption {
	return func(r *Router) { r.pending[name] = b }
}

// NewRouter returns a Router over the endpoints in declaration order. Every
// endpoint gets its own default Breaker, so one endpoint's failures never
// affect another's. An empty endpoint list is rejected.
func NewRouter(models []types.Model, opts ...RouterOption) (*Router, error) {
	if len(models) == 0 {
		return nil, errors.New("gohan/route: at least one endpoint is required")
	}
	r := &Router{
		models:   append([]types.Model(nil), models...),
		breakers: make(map[string]*Breaker, len(models)),
		pending:  make(map[string]*Breaker),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.classful {
		ordered := make([]types.Model, 0, len(r.models))
		for _, m := range r.models {
			if m.Profile().LatencyClass == r.class {
				ordered = append(ordered, m)
			}
		}
		for _, m := range r.models {
			if m.Profile().LatencyClass != r.class {
				ordered = append(ordered, m)
			}
		}
		r.models = ordered
	}
	for _, m := range r.models {
		name := m.Profile().Name
		if _, dup := r.breakers[name]; dup {
			continue
		}
		if b, injected := r.pending[name]; injected {
			r.breakers[name] = b
			continue
		}
		r.breakers[name] = NewBreaker()
	}
	return r, nil
}

// Candidates returns the endpoints in strategy order, skipping the ones
// whose breaker is open. When every breaker is open it returns nil: the call
// fails fast instead of hammering an endpoint the breaker has condemned.
func (r *Router) Candidates() []types.Model {
	var out []types.Model
	for _, m := range r.models {
		if r.breakers[m.Profile().Name].Allow() {
			out = append(out, m)
		}
	}
	return out
}

// Breaker returns the endpoint's breaker, so a caller can tune its window or
// observe its state.
func (r *Router) Breaker(name string) *Breaker {
	return r.breakers[name]
}

// RecordFailure feeds a failed call into the endpoint's breaker.
func (r *Router) RecordFailure(name string, err error) {
	if b, ok := r.breakers[name]; ok {
		b.Record(err)
	}
}

// RecordSuccess closes the endpoint's breaker after a healthy call.
func (r *Router) RecordSuccess(name string) {
	if b, ok := r.breakers[name]; ok {
		b.Succeeded()
	}
}
