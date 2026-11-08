package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/config"
)

func TestBuildReportURL(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		reportID string
		want     string
	}{
		{
			name:     "absolute configured base",
			baseURL:  "https://audit.example.test/",
			reportID: "report-123",
			want:     "https://audit.example.test/reports/report-123",
		},
		{
			name:     "configured path prefix",
			baseURL:  "https://audit.example.test/workspace",
			reportID: "report-123",
			want:     "https://audit.example.test/workspace/reports/report-123",
		},
		{
			name:     "relative without public base",
			reportID: "report 123",
			want:     "/reports/report%20123",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := buildReportURL(test.baseURL, test.reportID); got != test.want {
				t.Fatalf("buildReportURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGenerateAuditDossierRequiresTenantContext(t *testing.T) {
	server := NewServer(ServerInfo{Name: "test", Version: "test"})
	RegisterGatewayTools(server, config.Default())
	tool, ok := server.registry.GetTool("generate_audit_dossier")
	if !ok {
		t.Fatal("generate_audit_dossier tool not registered")
	}
	_, err := tool.Handler(context.Background(), map[string]interface{}{
		"claims": []interface{}{"claim"},
	})
	if err == nil || !strings.Contains(err.Error(), "tenant context required") {
		t.Fatalf("error = %v, want missing-tenant error", err)
	}
}
