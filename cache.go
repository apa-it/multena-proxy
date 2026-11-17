package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// cachedLabels is the value stored per cache entry: the result of a
// successful Labelstore.GetLabels call. There is deliberately no field (or
// sentinel) for an error state - CachingLabelStore never calls Set when the
// underlying lookup failed, so an error can never be represented in the
// cache in the first place.
type cachedLabels struct {
	Labels map[string]bool `json:"labels"`
	Skip   bool            `json:"skip"`
}

// LabelCache is the storage backend used by CachingLabelStore. Implementations
// (in-memory, Redis, ...) only need to support a basic get/set-with-TTL
// contract; CachingLabelStore owns all cache-vs-backend decision logic.
type LabelCache interface {
	Get(ctx context.Context, key string) (cachedLabels, bool, error)
	Set(ctx context.Context, key string, value cachedLabels, ttl time.Duration) error
}

var (
	labelCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "multena_labelstore_cache_hits_total",
		Help: "Number of labelstore permission lookups served from the TTL cache.",
	})
	labelCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "multena_labelstore_cache_misses_total",
		Help: "Number of labelstore permission lookups that required a backend call (cache miss or disabled cache).",
	})
	labelCacheErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "multena_labelstore_cache_errors_total",
		Help: "Number of errors encountered while reading from or writing to the labelstore cache backend.",
	})
)

// CachingLabelStore wraps a Labelstore with a TTL cache in front of
// GetLabels. Only successful lookups are ever cached: if the underlying
// store returns an error, CachingLabelStore propagates it unchanged and
// skips the cache write, so a transient backend outage can never be
// mistaken for a cached "no labels" (or any other) result for the TTL
// duration - that would be a security/availability regression.
type CachingLabelStore struct {
	Labelstore
	cache LabelCache
	ttl   time.Duration
}

// NewCachingLabelStore builds a CachingLabelStore wrapping underlying with
// cache, using ttl as the per-entry expiration passed to cache.Set.
func NewCachingLabelStore(underlying Labelstore, cache LabelCache, ttl time.Duration) *CachingLabelStore {
	return &CachingLabelStore{Labelstore: underlying, cache: cache, ttl: ttl}
}

// GetLabels serves from the cache on a hit, otherwise calls through to the
// wrapped Labelstore and (only on success) populates the cache for
// subsequent lookups of the same identity.
func (c *CachingLabelStore) GetLabels(ctx context.Context, token OAuthToken) (map[string]bool, bool, error) {
	key := labelCacheKey(token)

	cached, found, err := c.cache.Get(ctx, key)
	if err != nil {
		labelCacheErrors.Inc()
		log.Warn().Err(err).Msg("Error reading labelstore cache entry, falling back to backend lookup")
	} else if found {
		labelCacheHits.Inc()
		return cached.Labels, cached.Skip, nil
	}

	labelCacheMisses.Inc()
	labels, skip, err := c.Labelstore.GetLabels(ctx, token)
	if err != nil {
		// Never cache errors - see the type doc comment above.
		return labels, skip, err
	}

	if setErr := c.cache.Set(ctx, key, cachedLabels{Labels: labels, Skip: skip}, c.ttl); setErr != nil {
		labelCacheErrors.Inc()
		log.Warn().Err(setErr).Msg("Error writing labelstore cache entry")
	}
	return labels, skip, nil
}

// labelCacheKey derives a stable cache key from the token fields any
// labelstore backend may use for lookup (username, email, groups), so the
// same cache implementation works regardless of which field db.token_key
// (or the configmap keying) actually relies on.
func labelCacheKey(token OAuthToken) string {
	groups := append([]string(nil), token.Groups...)
	sort.Strings(groups)
	raw := strings.Join([]string{token.PreferredUsername, token.Email, strings.Join(groups, ",")}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// memoryLabelCache is the default, in-memory LabelCache backend, backed by
// patrickmn/go-cache. It requires no external dependency or network hop and
// is the right default for a single proxy instance.
type memoryLabelCache struct {
	c *gocache.Cache
}

// NewMemoryLabelCache creates an in-memory cache with the given default
// expiration and periodic-cleanup interval.
func NewMemoryLabelCache(defaultExpiration, cleanupInterval time.Duration) *memoryLabelCache {
	return &memoryLabelCache{c: gocache.New(defaultExpiration, cleanupInterval)}
}

func (m *memoryLabelCache) Get(_ context.Context, key string) (cachedLabels, bool, error) {
	v, found := m.c.Get(key)
	if !found {
		return cachedLabels{}, false, nil
	}
	cl, ok := v.(cachedLabels)
	if !ok {
		return cachedLabels{}, false, fmt.Errorf("unexpected cache value type %T for key", v)
	}
	return cl, true, nil
}

func (m *memoryLabelCache) Set(_ context.Context, key string, value cachedLabels, ttl time.Duration) error {
	m.c.Set(key, value, ttl)
	return nil
}

// RedisConfig holds the connection settings for the optional Redis-backed
// LabelCache implementation.
type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// redisLabelCache is the opt-in, cross-instance LabelCache backend, backed
// by redis/go-redis/v9. Selecting it requires an explicit
// labelstore_cache.backend: redis plus a valid labelstore_cache.redis
// connection config; see NewRedisLabelCache.
type redisLabelCache struct {
	client *redis.Client
}

// NewRedisLabelCache validates cfg and establishes (and verifies, via PING)
// a connection to Redis. It returns an error - never a silent no-op cache -
// if the address is missing or the server is unreachable, so misconfiguring
// the redis backend fails the startup fast instead of degrading silently to
// "no caching".
func NewRedisLabelCache(cfg RedisConfig) (*redisLabelCache, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, errors.New("labelstore_cache.redis.address must be set when labelstore_cache.backend is \"redis\"")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Address,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("could not connect to redis labelstore cache at %q: %w", cfg.Address, err)
	}

	return &redisLabelCache{client: client}, nil
}

func (r *redisLabelCache) Get(ctx context.Context, key string) (cachedLabels, bool, error) {
	raw, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return cachedLabels{}, false, nil
	}
	if err != nil {
		return cachedLabels{}, false, err
	}
	var cl cachedLabels
	if err := json.Unmarshal(raw, &cl); err != nil {
		return cachedLabels{}, false, fmt.Errorf("error decoding cached labelstore entry: %w", err)
	}
	return cl, true, nil
}

func (r *redisLabelCache) Set(ctx context.Context, key string, value cachedLabels, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("error encoding labelstore cache entry: %w", err)
	}
	return r.client.Set(ctx, key, raw, ttl).Err()
}
