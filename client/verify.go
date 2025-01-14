package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/drand/drand/chain"
	"github.com/drand/drand/log"
)

// newVerifyingClient wraps a client to perform `chain.Verify` on emitted results.
// v2from indicates from which round to verify the v2 signature only. Before
// that round, the client only verifies the v1.
func newVerifyingClient(c Client, previousResult Result, strict bool, v2from uint64) Client {
	return &verifyingClient{
		Client:         c,
		indirectClient: c,
		pointOfTrust:   previousResult,
		strict:         strict,
		v2from:         v2from,
	}
}

type verifyingClient struct {
	// Client is the wrapped client. calls to `get` and `watch` return results proxied from this client's fetch
	Client

	// indirectClient is used to fetch other rounds of randomness needed for verification.
	// it is separated so that it can provide a cache or shared pool that the direct client may not.
	indirectClient Client

	pointOfTrust Result
	potLk        sync.Mutex
	strict       bool

	log    log.Logger
	v2from uint64
}

// SetLog configures the client log output.
func (v *verifyingClient) SetLog(l log.Logger) {
	v.log = l
}

// Get returns a requested round of randomness
func (v *verifyingClient) Get(ctx context.Context, round uint64) (Result, error) {
	info, err := v.indirectClient.Info(ctx)
	if err != nil {
		return nil, err
	}
	r, err := v.Client.Get(ctx, round)
	if err != nil {
		return nil, err
	}
	rd := v.asRandomData(r)
	if err := v.verify(ctx, info, rd); err != nil {
		return nil, err
	}
	return rd, nil
}

// Watch returns new randomness as it becomes available.
func (v *verifyingClient) Watch(ctx context.Context) <-chan Result {
	outCh := make(chan Result, 1)

	info, err := v.indirectClient.Info(ctx)
	if err != nil {
		v.log.Error("verifying_client", "could not get info", "err", err)
		close(outCh)
		return outCh
	}

	inCh := v.Client.Watch(ctx)
	go func() {
		defer close(outCh)
		for r := range inCh {
			if err := v.verify(ctx, info, v.asRandomData(r)); err != nil {
				v.log.Warn("verifying_client", "skipping invalid watch round", "round", r.Round(), "err", err)
				continue
			}
			outCh <- r
		}
	}()
	return outCh
}

type resultWithPreviousSignature interface {
	PreviousSignature() []byte
}

func (v *verifyingClient) asRandomData(r Result) *RandomData {
	rd, ok := r.(*RandomData)
	if ok {
		return rd
	}
	s := r.Signature()
	rd = &RandomData{
		Rnd:    r.Round(),
		Random: r.Randomness(),
	}
	if r.Round() >= v.v2from {
		rd.SigV2 = s
		rd.version = 2
	} else {
		rd.Sig = s
	}
	if rp, ok := r.(resultWithPreviousSignature); ok {
		rd.PreviousSignature = rp.PreviousSignature()
	}

	return rd
}

func (v *verifyingClient) getTrustedPreviousSignature(ctx context.Context, round uint64) ([]byte, error) {
	info, err := v.indirectClient.Info(ctx)
	if err != nil {
		v.log.Error("drand_client", "could not get info to verify round 1", "err", err)
		return []byte{}, fmt.Errorf("could not get info: %w", err)
	}

	if round == 1 {
		return info.GroupHash, nil
	}

	trustRound := uint64(1)
	var trustPrevSig []byte
	b := chain.Beacon{}

	v.potLk.Lock()
	if v.pointOfTrust == nil || v.pointOfTrust.Round() > round {
		// slow path
		v.potLk.Unlock()
		trustPrevSig, err = v.getTrustedPreviousSignature(ctx, 1)
		if err != nil {
			return nil, err
		}
	} else {
		trustRound = v.pointOfTrust.Round()
		trustPrevSig = v.pointOfTrust.Signature()
		v.potLk.Unlock()
	}
	initialTrustRound := trustRound

	var next Result
	for trustRound < round-1 {
		trustRound++
		v.log.Debug("verifying_client", "loading round to verify", "round", trustRound)
		next, err = v.indirectClient.Get(ctx, trustRound)
		if err != nil {
			return []byte{}, fmt.Errorf("could not get round %d: %w", trustRound, err)
		}
		b.PreviousSig = trustPrevSig
		b.Round = trustRound
		b.Signature = next.Signature()

		ipk := info.PublicKey.Clone()
		if err := chain.VerifyBeacon(ipk, &b); err != nil {
			v.log.Warn("verifying_client", "failed to verify value", "b", b, "err", err)
			return []byte{}, fmt.Errorf("verifying beacon: %w", err)
		}
		trustPrevSig = next.Signature()
	}
	if trustRound == round-1 && trustRound > initialTrustRound {
		v.potLk.Lock()
		v.pointOfTrust = next
		v.potLk.Unlock()
	}

	if trustRound != round-1 {
		return []byte{}, fmt.Errorf("unexpected trust round %d", trustRound)
	}
	return trustPrevSig, nil
}

func (v *verifyingClient) verify(ctx context.Context, info *chain.Info, r *RandomData) (err error) {
	ps := r.PreviousSignature
	if r.Round() < v.v2from && (v.strict || r.PreviousSignature == nil) {
		ps, err = v.getTrustedPreviousSignature(ctx, r.Round())
		if err != nil {
			return
		}
	}

	ipk := info.PublicKey.Clone()
	if r.Round() >= v.v2from {
		b := chain.Beacon{
			PreviousSig: ps,
			Round:       r.Round(),
			SignatureV2: r.SigV2,
		}

		if err := chain.VerifyBeaconV2(ipk, &b); err != nil {
			return fmt.Errorf("verification v2 of %s failed: %w", b.String(), err)
		}
		r.Random = chain.RandomnessFromSignature(r.SigV2)
	} else {
		b := chain.Beacon{
			PreviousSig: ps,
			Round:       r.Round(),
			Signature:   r.Signature(),
		}
		if err = chain.VerifyBeacon(ipk, &b); err != nil {
			return fmt.Errorf("verification v1 of %s failed: %w", b.String(), err)
		}
		r.Random = chain.RandomnessFromSignature(r.Sig)
	}
	return nil
}

// String returns the name of this client.
func (v *verifyingClient) String() string {
	return fmt.Sprintf("%s.(+verifier)", v.Client)
}
