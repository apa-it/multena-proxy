package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingLabelStore is a fake Labelstore that counts GetLabels calls and
// can be programmed to fail on the next N calls, so tests can distinguish
// "served from cache" (call count does not increase) from "fell through to
// the backend" (call count increases).
type countingLabelStore struct {
	calls     int64
	labels    map[string]bool
	skip      bool
	failTimes int // number of upcoming calls that should return an error
}

func (c *countingLabelStore) Connect(App) error { return nil }

func (c *countingLabelStore) GetLabels(_ context.Context, _ OAuthToken) (map[string]bool, bool, error) {
	atomic.AddInt64(&c.calls, 1)
	if c.failTimes > 0 {
		c.failTimes--
		return nil, false, errors.New("backend unavailable")
	}
	return c.labels, c.skip, nil
}

func (c *countingLabelStore) callCount() int64 {
	return atomic.LoadInt64(&c.calls)
}

func testToken() OAuthToken {
	return OAuthToken{PreferredUsername: "alice", Email: "alice@example.com", Groups: []string{"team-a"}}
}

func TestCachingLabelStore_MissThenHit(t *testing.T) {
	backend := &countingLabelStore{labels: map[string]bool{"ns1": true}}
	cache := NewMemoryLabelCache(time.Minute, time.Minute)
	cls := NewCachingLabelStore(backend, cache, time.Minute)

	labels, skip, err := cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"ns1": true}, labels)
	assert.False(t, skip)
	assert.EqualValues(t, 1, backend.callCount(), "first call must go to the backend")

	// Second call for the same identity must be served from the cache and
	// must NOT call through to the backend again.
	labels, skip, err = cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"ns1": true}, labels)
	assert.False(t, skip)
	assert.EqualValues(t, 1, backend.callCount(), "second call must be served from cache, not the backend")
}

func TestCachingLabelStore_DifferentIdentitiesDoNotShareCacheEntries(t *testing.T) {
	backend := &countingLabelStore{labels: map[string]bool{"ns1": true}}
	cache := NewMemoryLabelCache(time.Minute, time.Minute)
	cls := NewCachingLabelStore(backend, cache, time.Minute)

	_, _, err := cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)

	other := OAuthToken{PreferredUsername: "bob", Email: "bob@example.com"}
	_, _, err = cls.GetLabels(context.Background(), other)
	require.NoError(t, err)

	assert.EqualValues(t, 2, backend.callCount(), "distinct identities must each trigger a backend lookup")
}

func TestCachingLabelStore_TTLExpiryTriggersFreshLookup(t *testing.T) {
	backend := &countingLabelStore{labels: map[string]bool{"ns1": true}}
	ttl := 30 * time.Millisecond
	cache := NewMemoryLabelCache(ttl, ttl)
	cls := NewCachingLabelStore(backend, cache, ttl)

	_, _, err := cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.EqualValues(t, 1, backend.callCount())

	time.Sleep(ttl + 50*time.Millisecond)

	_, _, err = cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.EqualValues(t, 2, backend.callCount(), "expired entry must trigger a fresh backend lookup")
}

func TestCachingLabelStore_ErrorsAreNeverCached(t *testing.T) {
	backend := &countingLabelStore{labels: map[string]bool{"ns1": true}, failTimes: 1}
	cache := NewMemoryLabelCache(time.Minute, time.Minute)
	cls := NewCachingLabelStore(backend, cache, time.Minute)

	// First call fails.
	_, _, err := cls.GetLabels(context.Background(), testToken())
	require.Error(t, err)
	assert.EqualValues(t, 1, backend.callCount())

	// The error must NOT have been cached: the next call must go to the
	// backend again (and this time succeed), not be served a cached error
	// or an empty/successful result derived from the failed call.
	labels, skip, err := cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"ns1": true}, labels)
	assert.False(t, skip)
	assert.EqualValues(t, 2, backend.callCount(), "a failed lookup must not be cached - the retry must reach the backend")
}

func TestCachingLabelStore_ClusterWideSkipIsCached(t *testing.T) {
	backend := &countingLabelStore{labels: nil, skip: true}
	cache := NewMemoryLabelCache(time.Minute, time.Minute)
	cls := NewCachingLabelStore(backend, cache, time.Minute)

	_, skip, err := cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.True(t, skip)

	_, skip, err = cls.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.True(t, skip)
	assert.EqualValues(t, 1, backend.callCount(), "a successful cluster-wide skip result is also cacheable")
}

func TestMemoryLabelCache_GetMiss(t *testing.T) {
	cache := NewMemoryLabelCache(time.Minute, time.Minute)
	_, found, err := cache.Get(context.Background(), "missing-key")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestLabelCacheKey_StableAndDistinctPerIdentity(t *testing.T) {
	a := labelCacheKey(OAuthToken{PreferredUsername: "alice", Groups: []string{"b", "a"}})
	b := labelCacheKey(OAuthToken{PreferredUsername: "alice", Groups: []string{"a", "b"}})
	assert.Equal(t, a, b, "group order must not affect the cache key")

	c := labelCacheKey(OAuthToken{PreferredUsername: "bob", Groups: []string{"a", "b"}})
	assert.NotEqual(t, a, c, "different identities must map to different keys")
}

func TestNewRedisLabelCache_RequiresAddress(t *testing.T) {
	_, err := NewRedisLabelCache(RedisConfig{})
	require.Error(t, err, "missing redis address must fail fast, not silently skip caching")
}

func TestNewRedisLabelCache_FailsFastOnUnreachableServer(t *testing.T) {
	_, err := NewRedisLabelCache(RedisConfig{Address: "127.0.0.1:1"})
	require.Error(t, err, "an unreachable redis server must be a startup error, not a silent no-op cache")
}

func TestWrapLabelStoreWithCache_DisabledReturnsUnderlyingStore(t *testing.T) {
	backend := &countingLabelStore{}
	cfg := &Config{LabelStoreCache: LabelStoreCacheConfig{Enabled: false}}
	wrapped := wrapLabelStoreWithCache(backend, cfg)
	assert.Same(t, Labelstore(backend), wrapped)
}

func TestWrapLabelStoreWithCache_ConfigMapBackendNeverWrapped(t *testing.T) {
	backend := &countingLabelStore{}
	cfg := &Config{
		Web:             WebConfig{LabelStoreKind: "configmap"},
		LabelStoreCache: LabelStoreCacheConfig{Enabled: true, Backend: "memory", TTL: time.Minute},
	}
	wrapped := wrapLabelStoreWithCache(backend, cfg)
	assert.Same(t, Labelstore(backend), wrapped, "configmap backend must never be wrapped with a TTL cache")
}

func TestWrapLabelStoreWithCache_MySQLBackendWrappedWithMemoryCacheByDefault(t *testing.T) {
	backend := &countingLabelStore{labels: map[string]bool{"ns1": true}}
	cfg := &Config{
		Web:             WebConfig{LabelStoreKind: "mysql"},
		LabelStoreCache: LabelStoreCacheConfig{Enabled: true, Backend: "memory", TTL: time.Minute},
	}
	wrapped := wrapLabelStoreWithCache(backend, cfg)

	_, ok := wrapped.(*CachingLabelStore)
	require.True(t, ok, "mysql backend with caching enabled must be wrapped in a CachingLabelStore")

	_, _, err := wrapped.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	_, _, err = wrapped.GetLabels(context.Background(), testToken())
	require.NoError(t, err)
	assert.EqualValues(t, 1, backend.callCount(), "cache must be functional end-to-end through the wrapper")
}
