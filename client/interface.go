package client

import (
	"context"
	"time"

	"github.com/drand/drand/chain"
)

// Client represents the drand Client interface.
type Client interface {
	// Get returns a the randomness at `round` or an error.
	// Requesting round = 0 will return randomness for the most
	// recent known round, bounded at a minimum to the `RoundAt(time.Now())`
	Get(ctx context.Context, round uint64) (Result, error)

	// Watch returns new randomness as it becomes available.
	Watch(ctx context.Context) <-chan Result

	// Info returns the parameters of the chain this client is connected to.
	// The public key, when it started, and how frequently it updates.
	Info(ctx context.Context) (*chain.Info, error)

	// RoundAt will return the most recent round of randomness that will be available
	// at time for the current client.
	RoundAt(time time.Time) uint64
}

// Result represents the randomness for a single drand round.
type Result interface {
	Round() uint64
	Randomness() []byte
	Signature() []byte
}
