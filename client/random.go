package client

// RandomData holds the full random response from the server, including data needed
// for validation.
type RandomData struct {
	Rnd               uint64 `json:"round,omitempty"`
	Random            []byte `json:"randomness,omitempty"`
	Sig               []byte `json:"signature,omitempty"`
	PreviousSignature []byte `json:"previous_signature,omitempty"`
	SigV2             []byte `json:"signaturev2,omitempty"`
	version           byte
}

// Round provides access to the round associatted with this random data.
func (r *RandomData) Round() uint64 {
	return r.Rnd
}

// Signature provides the signature over this round's randomness
func (r *RandomData) Signature() []byte {
	if r.version == 2 {
		return r.SigV2
	}
	return r.Sig
}

// Randomness exports the randomness
func (r *RandomData) Randomness() []byte {
	return r.Random
}
