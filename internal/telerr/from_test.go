package telerr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// A domain error leaves as itself, wherever in the chain it is.
func TestFrom_KeepsADomainError(t *testing.T) {
	e := &telerr.Error{Kind: telerr.Network, Op: "dial"}

	assert.Same(t, e, telerr.From(fmt.Errorf("connect: %w", e)))
}

// Anything else becomes an Internal error saying what it said, with the
// original kept for whoever is in the same process.
func TestFrom_TurnsAnyOtherErrorIntoAnInternalOne(t *testing.T) {
	raw := errors.New("dial tcp: connection refused")

	got := telerr.From(raw)

	assert.Equal(t, telerr.Internal, got.Kind)
	assert.Equal(t, "dial tcp: connection refused", got.Detail)
	assert.ErrorIs(t, got, raw)
}

func TestFrom_NilStaysNil(t *testing.T) {
	assert.Nil(t, telerr.From(nil))
}

// An error that names nothing but its own text is said once, whether it
// arrives raw or as the Internal error From made of it.
func TestUnnamed(t *testing.T) {
	raw := errors.New("connection refused")

	said, unnamed := telerr.Unnamed(raw)
	assert.True(t, unnamed)
	assert.Equal(t, "connection refused", said)

	said, unnamed = telerr.Unnamed(telerr.From(raw))
	assert.True(t, unnamed)
	assert.Equal(t, "connection refused", said)

	_, unnamed = telerr.Unnamed(&telerr.Error{Kind: telerr.Internal, Op: "users.getUsers", Detail: "X"})
	assert.False(t, unnamed, "an Internal error from an operation has a name")
	_, unnamed = telerr.Unnamed(&telerr.Error{Kind: telerr.Network})
	assert.False(t, unnamed)
}
