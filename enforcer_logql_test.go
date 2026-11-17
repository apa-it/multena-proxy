package main

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
)

func TestLogqlEnforcer(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		tenantLabels   map[string]bool
		expectedResult string
		expectErr      bool
	}{
		{
			name:           "Valid query and tenant labels",
			query:          "{kubernetes_namespace_name=\"test\"}",
			tenantLabels:   map[string]bool{"test": true},
			expectedResult: "{kubernetes_namespace_name=\"test\"}",
			expectErr:      false,
		},
		{
			name:           "Empty query and valid tenant labels",
			query:          "",
			tenantLabels:   map[string]bool{"test": true},
			expectedResult: "{kubernetes_namespace_name=\"test\"}",
			expectErr:      false,
		},
		{
			name:         "Valid query and invalid tenant labels",
			query:        "{kubernetes_namespace_name=\"test\"}",
			tenantLabels: map[string]bool{"invalid": true},
			expectErr:    true,
		},
	}

	enforcer := LogQLEnforcer{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := enforcer.Enforce(tt.query, tt.tenantLabels, "kubernetes_namespace_name")
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

// TestLogqlEnforcerNegativeMatchers proves the tenant-bypass via negative LogQL
// matchers on the tenant label (!= and !~) is rejected, while positive matchers
// and label-combinations keep working as before.
func TestLogqlEnforcerNegativeMatchers(t *testing.T) {
	tenantLabels := map[string]bool{"my-ns": true}
	labelMatch := "kubernetes_namespace_name"

	tests := []struct {
		name      string
		query     string
		expectErr bool
	}{
		{
			name:      "negative equal matcher on tenant label is rejected",
			query:     `{kubernetes_namespace_name!="my-ns"}`,
			expectErr: true,
		},
		{
			name:      "negative regexp matcher on tenant label is rejected",
			query:     `{kubernetes_namespace_name!~"my-ns"}`,
			expectErr: true,
		},
		{
			name:      "positive equal matcher on tenant label is allowed",
			query:     `{kubernetes_namespace_name="my-ns"}`,
			expectErr: false,
		},
		{
			name:      "positive regexp matcher with unauthorized alternative is rejected",
			query:     `{kubernetes_namespace_name=~"my-ns|.*"}`,
			expectErr: true,
		},
		{
			name:      "negative matcher on non-tenant label combined with positive tenant matcher is allowed",
			query:     `{app!="excluded", kubernetes_namespace_name="my-ns"}`,
			expectErr: false,
		},
		{
			name:      "positive and negative matcher on tenant label is rejected",
			query:     `{kubernetes_namespace_name="my-ns", kubernetes_namespace_name!="other"}`,
			expectErr: true,
		},
	}

	enforcer := LogQLEnforcer{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enforcer.Enforce(tt.query, tenantLabels, labelMatch)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMatchNamespaceMatchers(t *testing.T) {
	tests := []struct {
		name         string
		matchers     []*labels.Matcher
		tenantLabels map[string]bool
		expectErr    bool
	}{
		{
			name: "Valid matchers and tenant labels",
			matchers: []*labels.Matcher{
				{
					Type:  labels.MatchEqual,
					Name:  "kubernetes_namespace_name",
					Value: "test",
				},
			},
			tenantLabels: map[string]bool{"test": true},
			expectErr:    false,
		},
		{
			name: "Invalid matchers and valid tenant labels",
			matchers: []*labels.Matcher{
				{
					Type:  labels.MatchEqual,
					Name:  "kubernetes_namespace_name",
					Value: "invalid",
				},
			},
			tenantLabels: map[string]bool{"test": true},
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MatchTenantLabelMatchers(tt.matchers, tt.tenantLabels, "kubernetes_namespace_name")
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
