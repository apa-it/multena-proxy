package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLabelsCM(t *testing.T) {
	cmh := ConfigMapHandler{
		labels: map[string]map[string]bool{
			"user1":      {"u1": true, "u2": true},
			"user2":      {"u3": true, "u4": true},
			"group1":     {"g1": true, "g2": true},
			"group2":     {"g3": true, "g4": true},
			"adminGroup": {"#cluster-wide": true, "g4": true},
		},
	}

	cases := []struct {
		name     string
		username string
		groups   []string
		expected map[string]bool
		skip     bool
	}{
		{
			name:     "User with groups",
			username: "user1",
			groups:   []string{"group1", "group2"},
			expected: map[string]bool{
				"u1": true,
				"u2": true,
				"g1": true,
				"g2": true,
				"g3": true,
				"g4": true,
			},
		},
		{
			name:     "User without groups",
			username: "user2",
			groups:   []string{},
			expected: map[string]bool{
				"u3": true,
				"u4": true,
			},
		},
		{
			name:     "Non-existent user",
			username: "user3",
			groups:   []string{"group1"},
			expected: map[string]bool{
				"g1": true,
				"g2": true,
			},
		},
		{
			name:     "Non-existent group",
			username: "user1",
			groups:   []string{"group3"},
			expected: map[string]bool{
				"u1": true,
				"u2": true,
			},
		},
		{
			name:     "admin_group",
			username: "blubb",
			groups:   []string{"adminGroup"},
			expected: nil,
			skip:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels, skip, err := cmh.GetLabels(context.Background(), OAuthToken{PreferredUsername: tc.username, Groups: tc.groups})
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, labels)
			assert.Equal(t, tc.skip, skip)
		})
	}
}

// TestConfigMapHandler_ConcurrentReloadAndRead exercises the fsnotify reload
// path concurrently with request-path reads (GetLabels). Run with -race: it
// must never trip the race detector, proving the c.labels map is properly
// guarded by the RWMutex (Problem B).
func TestConfigMapHandler_ConcurrentReloadAndRead(t *testing.T) {
	cmh := &ConfigMapHandler{
		labels: map[string]map[string]bool{
			"user1": {"u1": true},
		},
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Simulate concurrent requests reading labels.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _, err := cmh.GetLabels(context.Background(), OAuthToken{PreferredUsername: "user1"})
					assert.NoError(t, err)
				}
			}
		}()
	}

	// Simulate concurrent config reloads (what v.OnConfigChange does).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			newLabels := map[string]map[string]bool{
				"user1": {"u1": true, "u2": true},
			}
			cmh.mu.Lock()
			cmh.labels = newLabels
			cmh.mu.Unlock()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// mockTokenMapTestHelper builds an OAuthToken for the given token key value.
func newMySQLHandler(t *testing.T, tokenKey, query string) (*MySQLHandler, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return &MySQLHandler{
		DB:       db,
		Query:    query,
		TokenKey: tokenKey,
	}, mock, func() { _ = db.Close() }
}

func TestMySQLHandler_GetLabels_Success(t *testing.T) {
	m, mock, closeDB := newMySQLHandler(t, "username", "SELECT namespace FROM labels WHERE username = ?")
	defer closeDB()

	rows := sqlmock.NewRows([]string{"namespace"}).AddRow("ns1").AddRow("ns2")
	mock.ExpectQuery("SELECT namespace FROM labels WHERE username = ?").WithArgs("alice").WillReturnRows(rows)

	labels, skip, err := m.GetLabels(context.Background(), OAuthToken{PreferredUsername: "alice"})

	require.NoError(t, err)
	assert.False(t, skip)
	assert.Equal(t, map[string]bool{"ns1": true, "ns2": true}, labels)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_GetLabels_UnsupportedTokenKey(t *testing.T) {
	m, mock, closeDB := newMySQLHandler(t, "unsupported_prop", "SELECT namespace FROM labels WHERE x = ?")
	defer closeDB()

	labels, skip, err := m.GetLabels(context.Background(), OAuthToken{PreferredUsername: "alice"})

	// Must NOT exit the process - must return an error instead.
	require.Error(t, err)
	assert.False(t, skip)
	assert.Nil(t, labels)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_GetLabels_QueryError(t *testing.T) {
	m, mock, closeDB := newMySQLHandler(t, "username", "SELECT namespace FROM labels WHERE username = ?")
	defer closeDB()

	mock.ExpectQuery("SELECT namespace FROM labels WHERE username = ?").
		WithArgs("alice").
		WillReturnError(errors.New("connection refused"))

	labels, skip, err := m.GetLabels(context.Background(), OAuthToken{PreferredUsername: "alice"})

	// Must NOT exit the process - a DB hiccup must yield an error, not os.Exit.
	require.Error(t, err)
	assert.False(t, skip)
	assert.Nil(t, labels)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_GetLabels_ScanError(t *testing.T) {
	m, mock, closeDB := newMySQLHandler(t, "username", "SELECT namespace FROM labels WHERE username = ?")
	defer closeDB()

	// A non-matching column count forces a Scan error on res.Next()/Scan().
	rows := sqlmock.NewRows([]string{"namespace", "extra"}).AddRow("ns1", "x")
	mock.ExpectQuery("SELECT namespace FROM labels WHERE username = ?").WithArgs("alice").WillReturnRows(rows)

	labels, skip, err := m.GetLabels(context.Background(), OAuthToken{PreferredUsername: "alice"})

	require.Error(t, err)
	assert.False(t, skip)
	assert.Nil(t, labels)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_GetLabels_NoRows(t *testing.T) {
	m, mock, closeDB := newMySQLHandler(t, "username", "SELECT namespace FROM labels WHERE username = ?")
	defer closeDB()

	rows := sqlmock.NewRows([]string{"namespace"})
	mock.ExpectQuery("SELECT namespace FROM labels WHERE username = ?").WithArgs("alice").WillReturnRows(rows)

	labels, skip, err := m.GetLabels(context.Background(), OAuthToken{PreferredUsername: "alice"})

	require.NoError(t, err)
	assert.False(t, skip)
	assert.Equal(t, map[string]bool{}, labels)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_Close(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	m := &MySQLHandler{DB: db}

	err = m.Close()

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMySQLHandler_Close_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose().WillReturnError(errors.New("close failed"))
	m := &MySQLHandler{DB: db}

	err = m.Close()

	// Must NOT exit the process - a close failure must be returned as an error.
	assert.Error(t, err)
}
