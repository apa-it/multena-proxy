package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/fsnotify/fsnotify"
	"github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"
)

// maxOpenDBConns and dbConnMaxLifetime bound the MySQL connection pool used
// by the label store so a misbehaving or slow database cannot exhaust
// connections or keep stale connections open indefinitely.
const (
	maxOpenDBConns    = 10
	dbConnMaxLifetime = time.Hour
)

// Labelstore represents an interface defining methods for connecting to a
// label store and retrieving labels associated with a given OAuth token.
type Labelstore interface {
	// Connect establishes a connection with the label store using App configuration.
	Connect(App) error
	// GetLabels retrieves labels associated with the provided OAuth token.
	// Returns a map containing the labels, a boolean indicating whether
	// the label is cluster-wide or not, and an error if the lookup failed.
	GetLabels(ctx context.Context, token OAuthToken) (map[string]bool, bool, error)
}

// WithLabelStore initializes and connects to a LabelStore specified in the
// application configuration. It assigns the connected LabelStore to the App
// instance and returns it. If the LabelStore type is unknown or an error
// occurs during the connection, it logs a fatal error.
//
// This runs once at startup (not in the request path), so a fatal exit here
// is an acceptable way to fail fast on misconfiguration.
func (a *App) WithLabelStore() *App {
	switch a.Cfg.Web.LabelStoreKind {
	case "configmap":
		a.LabelStore = &ConfigMapHandler{}
	case "mysql":
		a.LabelStore = &MySQLHandler{}
	default:
		log.Fatal().Str("type", a.Cfg.Web.LabelStoreKind).Msg("Unknown label store type")
	}
	err := a.LabelStore.Connect(*a)
	if err != nil {
		log.Fatal().Err(err).Msg("Error connecting to labelstore")
	}
	a.LabelStore = wrapLabelStoreWithCache(a.LabelStore, a.Cfg)
	return a
}

// wrapLabelStoreWithCache wraps ls with a TTL cache in front of GetLabels
// if labelstore_cache.enabled is set (the default). Misconfiguration of the
// cache backend (e.g. an unreachable/unset redis address) is a startup-time
// log.Fatal, not a silent fallback to "no caching" - the operator asked for
// caching and must find out immediately if it cannot be provided.
//
// The configmap backend is intentionally never wrapped: ConfigMapHandler
// already serves from an in-memory map guarded by a mutex (no round-trip to
// avoid) and is refreshed instantly via fsnotify on config changes. Putting
// a TTL cache in front of it would add complexity while introducing a real
// regression: a user whose access was just revoked in the ConfigMap could
// keep getting served the old, cached labels until the TTL expires.
func wrapLabelStoreWithCache(ls Labelstore, cfg *Config) Labelstore {
	cacheCfg := cfg.LabelStoreCache
	if !cacheCfg.Enabled {
		return ls
	}
	if cfg.Web.LabelStoreKind == "configmap" {
		log.Debug().Msg("Labelstore cache enabled but skipped for the configmap backend (no DB round-trip to avoid; caching would add reload staleness)")
		return ls
	}

	ttl := cacheCfg.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	var cache LabelCache
	switch cacheCfg.Backend {
	case "", "memory":
		cache = NewMemoryLabelCache(ttl, ttl*2)
	case "redis":
		redisCache, redisErr := NewRedisLabelCache(cacheCfg.Redis)
		if redisErr != nil {
			log.Fatal().Err(redisErr).Msg("Error connecting to redis labelstore cache backend")
		}
		cache = redisCache
	default:
		log.Fatal().Str("backend", cacheCfg.Backend).Msg("Unknown labelstore_cache.backend (must be \"memory\" or \"redis\")")
	}

	log.Info().Str("backend", cacheCfg.Backend).Dur("ttl", ttl).Msg("Labelstore cache enabled")
	return NewCachingLabelStore(ls, cache, ttl)
}

type ConfigMapHandler struct {
	mu     sync.RWMutex
	labels map[string]map[string]bool
}

func (c *ConfigMapHandler) Connect(_ App) error {
	v := viper.NewWithOptions(viper.KeyDelimiter("::"))
	v.SetConfigName("labels")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/config/labels/")
	v.AddConfigPath("./configs")
	err := v.MergeInConfig()
	if err != nil {
		return err
	}
	c.mu.Lock()
	err = v.Unmarshal(&c.labels)
	c.mu.Unlock()
	if err != nil {
		log.Fatal().Err(err).Msg("Error while unmarshalling config file")
		return err
	}
	v.OnConfigChange(func(e fsnotify.Event) {
		log.Info().Str("file", e.Name).Msg("Config file changed")
		if mergeErr := v.MergeInConfig(); mergeErr != nil {
			log.Error().Err(mergeErr).Msg("Error while reloading config file")
			return
		}
		var newLabels map[string]map[string]bool
		if unmarshalErr := v.Unmarshal(&newLabels); unmarshalErr != nil {
			log.Error().Err(unmarshalErr).Msg("Error while unmarshalling config file")
			return
		}
		c.mu.Lock()
		c.labels = newLabels
		c.mu.Unlock()
	})
	v.WatchConfig()
	log.Debug().Any("labels", c.labels).Msg("")
	return nil
}

func (c *ConfigMapHandler) GetLabels(_ context.Context, token OAuthToken) (map[string]bool, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	username := token.PreferredUsername
	groups := token.Groups
	mergedNamespaces := make(map[string]bool, len(c.labels[username])*2)
	for k := range c.labels[username] {
		mergedNamespaces[k] = true
		if k == "#cluster-wide" {
			return nil, true, nil
		}
	}
	for _, group := range groups {
		for k := range c.labels[group] {
			mergedNamespaces[k] = true
			if k == "#cluster-wide" {
				return nil, true, nil
			}
		}
	}
	return mergedNamespaces, false, nil
}

type MySQLHandler struct {
	DB       *sql.DB
	Query    string
	TokenKey string
}

func (m *MySQLHandler) Connect(a App) error {
	m.TokenKey = a.Cfg.Db.TokenKey
	m.Query = a.Cfg.Db.Query
	password, err := os.ReadFile(a.Cfg.Db.PasswordPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not read db password")
	}
	cfg := mysql.Config{
		User:                 a.Cfg.Db.User,
		Passwd:               string(password),
		Net:                  "tcp",
		AllowNativePasswords: true,
		Addr:                 fmt.Sprintf("%s:%d", a.Cfg.Db.Host, a.Cfg.Db.Port),
		DBName:               a.Cfg.Db.DbName,
	}
	// Get a database handle.
	m.DB, err = sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		log.Fatal().Err(err).Msg("Error opening DB connection")
	}
	m.DB.SetMaxOpenConns(maxOpenDBConns)
	m.DB.SetConnMaxLifetime(dbConnMaxLifetime)
	return nil
}

func (m *MySQLHandler) Close() error {
	err := m.DB.Close()
	if err != nil {
		log.Error().Err(err).Msg("Error closing DB connection")
		return err
	}
	return nil
}

func (m *MySQLHandler) GetLabels(ctx context.Context, token OAuthToken) (map[string]bool, bool, error) {
	tokenMap := map[string]string{
		"email":    token.Email,
		"username": token.PreferredUsername,
		"groups":   strings.Join(token.Groups, ","),
	}

	value, ok := tokenMap[m.TokenKey]
	if !ok {
		log.Error().Str("property", m.TokenKey).Msg("Unsupported token property")
		return nil, false, fmt.Errorf("unsupported token property: %s", m.TokenKey)
	}
	n := strings.Count(m.Query, "?")

	var params []any
	for i := 0; i < n; i++ {
		params = append(params, value)
	}

	res, err := m.DB.QueryContext(ctx, m.Query, params...)
	if err != nil {
		log.Error().Err(err).Str("query", m.Query).Msg("Error while querying database")
		return nil, false, fmt.Errorf("error querying database: %w", err)
	}
	defer func() {
		if closeErr := res.Close(); closeErr != nil {
			log.Error().Err(closeErr).Msg("Error closing DB result")
		}
	}()

	labels := make(map[string]bool)
	for res.Next() {
		var label string
		if err := res.Scan(&label); err != nil {
			log.Error().Err(err).Msg("Error scanning DB result")
			return nil, false, fmt.Errorf("error scanning DB result: %w", err)
		}
		labels[label] = true
	}
	if err := res.Err(); err != nil {
		log.Error().Err(err).Msg("Error iterating DB result")
		return nil, false, fmt.Errorf("error iterating DB result: %w", err)
	}
	return labels, false, nil
}
