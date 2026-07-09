//go:build !darwin

package signal

// NewSampler returns a Sampler that reports the platform is unsupported. This
// keeps the package buildable and vettable on non-darwin, though active-lens
// targets darwin/arm64 only.
func NewSampler() Sampler { return unsupportedSampler{} }

type unsupportedSampler struct{}

func (unsupportedSampler) Snapshot() (Snapshot, error) { return Snapshot{}, ErrUnsupported }
