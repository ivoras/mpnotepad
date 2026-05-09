package ulidgen

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns a new lexicographically sortable ULID.
func New() string {
	mu.Lock()
	defer mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}
