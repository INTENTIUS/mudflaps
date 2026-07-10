// Package id generates identifiers that mimic the shapes used by Fly's
// Machines API: short hex machine IDs and ULID-like uppercase instance IDs.
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// crockford is the uppercase alphabet used for instance IDs. It mirrors the
// look of Fly instance IDs, which resemble ULIDs.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Machine returns a 14-character hex identifier, matching the shape of Fly
// machine IDs (for example "148ed19d1d4986").
func Machine() string {
	b := make([]byte, 7)
	mustRead(b)
	return hex.EncodeToString(b)
}

// Instance returns a 26-character uppercase identifier, matching the shape of
// Fly instance IDs. Each call is unique, which is what lets an update mint a
// fresh instance ID for a new machine version.
func Instance() string {
	b := make([]byte, 26)
	mustRead(b)
	out := make([]byte, 26)
	for i := range b {
		out[i] = crockford[int(b[i])%len(crockford)]
	}
	return string(out)
}

// Nonce returns a random hex string suitable for use as a lease nonce.
func Nonce() string {
	b := make([]byte, 16)
	mustRead(b)
	return hex.EncodeToString(b)
}

func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on the platforms we target; if it
		// somehow does there is nothing sensible to recover to.
		panic("mudflaps: entropy source unavailable: " + err.Error())
	}
}
