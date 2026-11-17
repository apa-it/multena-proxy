package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWithSAT_DevModeSkipsServiceAccountFile proves that with dev mode
// enabled, WithSAT uses the configured static token and never attempts to
// read the Kubernetes service-account token file (which would log.Fatal and
// abort the process outside a real pod).
func TestWithSAT_DevModeSkipsServiceAccountFile(t *testing.T) {
	a := &App{Cfg: &Config{
		Dev: DevConfig{Enabled: true},
		Web: WebConfig{ServiceAccountToken: "dev-sat-value"},
	}}

	a = a.WithSAT()

	assert.Equal(t, "dev-sat-value", a.ServiceAccountToken)
}
