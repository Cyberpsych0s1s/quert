package redis

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cyberpsych0s1s/quert/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStore exercises the Redis deduplication store against a real RESP server
// (miniredis), proving the previously never-executed Redis code path.
func TestStore(t *testing.T) {
	mr := miniredis.RunT(t)
	s, err := New(Config{Addr: mr.Addr(), Prefix: "quert:"})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// IsSeen / MarkSeen roundtrip.
	seen, err := s.IsSeen(ctx, "http://x.test/a")
	require.NoError(t, err)
	assert.False(t, seen, "unseen initially")

	require.NoError(t, s.MarkSeen(ctx, "http://x.test/a"))
	seen, err = s.IsSeen(ctx, "http://x.test/a")
	require.NoError(t, err)
	assert.True(t, seen, "seen after MarkSeen")

	// StoreHash / GetOriginalURL roundtrip.
	require.NoError(t, s.StoreHash(ctx, "content", "deadbeef", "http://x.test/a"))
	got, err := s.GetOriginalURL(ctx, "content", "deadbeef")
	require.NoError(t, err)
	assert.Equal(t, "http://x.test/a", got)

	// Missing hash -> storage.ErrNotFound.
	_, err = s.GetOriginalURL(ctx, "content", "missing")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

// TestStorePersistsAcrossClients proves dedup state survives a new client
// (the basis for resumable crawls): a fresh Store against the same server sees
// keys written by an earlier one.
func TestStorePersistsAcrossClients(t *testing.T) {
	mr := miniredis.RunT(t)

	s1, err := New(Config{Addr: mr.Addr()})
	require.NoError(t, err)
	require.NoError(t, s1.MarkSeen(context.Background(), "http://x.test/seen"))
	require.NoError(t, s1.Close())

	s2, err := New(Config{Addr: mr.Addr()})
	require.NoError(t, err)
	defer func() { _ = s2.Close() }()

	seen, err := s2.IsSeen(context.Background(), "http://x.test/seen")
	require.NoError(t, err)
	assert.True(t, seen, "second client sees the first client's data")
}
