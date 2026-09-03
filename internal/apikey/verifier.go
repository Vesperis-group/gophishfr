package apikey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

const (
	// ProtocolVersion is the immutable verifier protocol version, independent
	// of application, release, and Git versions.
	ProtocolVersion = 1
	Domain          = "gophishfr-api-key-verifier:v1"
	VerifierSize    = sha256.Size
)

// Candidate is one indexed (key ID, verifier) lookup pair.
type Candidate struct {
	KeyID    string
	Verifier [VerifierSize]byte
}

// Service computes and validates protocol verifiers from an immutable keyring.
type Service struct {
	keyring *Keyring
}

func New(keyring *Keyring) (*Service, error) {
	if keyring == nil {
		return nil, ErrUnavailableKey
	}
	return &Service{keyring: keyring}, nil
}

func (s *Service) ActiveKeyID() string { return s.keyring.activeKeyID }

func (s *Service) KeyIDs() []string {
	result := make([]string, len(s.keyring.ids))
	copy(result, s.keyring.ids)
	return result
}

// Compute returns the raw 32-byte HMAC verifier for exact token bytes.
func (s *Service) Compute(keyID string, token []byte) ([VerifierSize]byte, error) {
	var result [VerifierSize]byte
	key, ok := s.keyring.keys[keyID]
	if !ok {
		return result, ErrUnavailableKey
	}
	return computeVerifier(key, Domain, token), nil
}

func computeVerifier(key [keySize]byte, domain string, token []byte) [VerifierSize]byte {
	var result [VerifierSize]byte
	mac := hmac.New(sha256.New, key[:])
	var length [8]byte
	// Length-prefix both protocol domain and exact token bytes to make framing
	// unambiguous for all byte sequences.
	binary.BigEndian.PutUint64(length[:], uint64(len(domain)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(domain))
	binary.BigEndian.PutUint64(length[:], uint64(len(token)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write(token)
	copy(result[:], mac.Sum(nil))
	return result
}

func (s *Service) ComputeActive(token []byte) (string, [VerifierSize]byte, error) {
	id := s.ActiveKeyID()
	verifier, err := s.Compute(id, token)
	return id, verifier, err
}

// Candidates computes the bounded indexed lookup set for every accepted key.
func (s *Service) Candidates(token []byte) ([]Candidate, error) {
	result := make([]Candidate, 0, len(s.keyring.ids))
	for _, id := range s.keyring.ids {
		verifier, err := s.Compute(id, token)
		if err != nil {
			return nil, err
		}
		result = append(result, Candidate{KeyID: id, Verifier: verifier})
	}
	return result, nil
}

// Verify compares a stored raw verifier using hmac.Equal.
func (s *Service) Verify(keyID string, token, stored []byte) error {
	if len(stored) != VerifierSize || !validKeyID(keyID) {
		return ErrInvalidState
	}
	expected, err := s.Compute(keyID, token)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected[:], stored) {
		return ErrVerification
	}
	return nil
}
