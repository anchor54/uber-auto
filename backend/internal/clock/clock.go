// Package clock is the time source shared by the API and the sweeper, so
// tests can move time for both at once.
package clock

import (
	"sync"
	"time"
)

// Clock tells the time.
type Clock interface {
	Now() time.Time
}

// Real is the system clock, to the microsecond (Postgres' precision).
type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Fake is a clock that only moves when told to. Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake is a fake clock at [t].
func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Set moves the clock to [t].
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.t = t.UTC()
	f.mu.Unlock()
}

// Advance moves the clock forward by [d].
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

// Offset is the real clock moved by an adjustable offset: the dev server's
// clock, which the simulator can move forward to close offers at once.
type Offset struct {
	mu sync.Mutex
	d  time.Duration
}

func (o *Offset) Now() time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return Real{}.Now().Add(o.d)
}

// Advance moves the clock forward by [d].
func (o *Offset) Advance(d time.Duration) {
	o.mu.Lock()
	o.d += d
	o.mu.Unlock()
}
