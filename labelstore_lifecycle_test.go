package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigMapHandler_Connect_LoadsFixtureLabels proves Connect loads and
// unmarshals the labels.yaml fixture under ./configs (the same path used in
// development/CI) into usable label data, and that the loaded data actually
// drives GetLabels correctly afterwards.
func TestConfigMapHandler_Connect_LoadsFixtureLabels(t *testing.T) {
	cmh := &ConfigMapHandler{}

	err := cmh.Connect(App{})

	require.NoError(t, err)

	// configs/labels.yaml grants group1 cluster-wide access.
	labels, skip, err := cmh.GetLabels(context.Background(), OAuthToken{PreferredUsername: "nobody", Groups: []string{"group1"}})
	require.NoError(t, err)
	assert.True(t, skip)
	assert.Nil(t, labels)
}

// TestWithLabelStore_ConfigMapKind proves the "configmap" branch of
// WithLabelStore wires up a ConfigMapHandler and successfully connects
// using the fixture config, without needing a Kubernetes/MySQL environment.
func TestWithLabelStore_ConfigMapKind(t *testing.T) {
	a := &App{Cfg: &Config{Web: WebConfig{LabelStoreKind: "configmap"}}}

	a = a.WithLabelStore()

	_, ok := a.LabelStore.(*ConfigMapHandler)
	assert.True(t, ok, "expected a *ConfigMapHandler to be wired up")
}

// TestWithLabelStore_MySQLKind proves the "mysql" branch of WithLabelStore
// wires up a MySQLHandler and successfully "connects" (sql.Open is lazy and
// does not require a reachable server) given a readable password file.
func TestWithLabelStore_MySQLKind(t *testing.T) {
	pwPath := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(pwPath, []byte("secret"), 0o600))

	a := &App{Cfg: &Config{
		Web: WebConfig{LabelStoreKind: "mysql"},
		Db: DbConfig{
			User:         "multena",
			PasswordPath: pwPath,
			Host:         "localhost",
			Port:         3306,
			DbName:       "example",
			Query:        "SELECT namespace FROM labels WHERE username = ?",
			TokenKey:     "username",
		},
	}}

	a = a.WithLabelStore()

	m, ok := a.LabelStore.(*MySQLHandler)
	require.True(t, ok, "expected a *MySQLHandler to be wired up")
	assert.NotNil(t, m.DB)
	assert.Equal(t, "username", m.TokenKey)
	assert.Equal(t, "SELECT namespace FROM labels WHERE username = ?", m.Query)
}
