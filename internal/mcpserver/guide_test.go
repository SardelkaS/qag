package mcpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPermanentGuideSurvivesArtifactExpiry(t *testing.T) {
	s := New(Options{})
	now := time.Now()
	s.Artifacts.now = func() time.Time { return now }
	files, err := s.Artifacts.PutGroup([]File{{Name: "expired.txt", MIMEType: "text/plain", Data: []byte("temporary")}})
	if err != nil {
		t.Fatal(err)
	}
	s.syncResources()
	now = now.Add(11 * time.Minute)
	s.syncResources()
	if _, err = s.Artifacts.Chunk(files[0].ID, 0, 1); err == nil {
		t.Fatal("artifact did not expire")
	}
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := mcp.NewClient(&mcp.Implementation{Name: "guide-test", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL}, &mcp.ClientSessionOptions{ProtocolVersion: version})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		list, err := session.ListResources(ctx, nil)
		if err != nil || len(list.Resources) != 1 || list.Resources[0].URI != GuideURI {
			t.Fatal("permanent discovery", err)
		}
		guide, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: GuideURI})
		if err != nil || len(guide.Contents) != 1 {
			t.Fatal("permanent read", err)
		}
		for _, tool := range s.Manifest() {
			if !strings.Contains(guide.Contents[0].Text, tool.Name) {
				t.Errorf("undocumented %s", tool.Name)
			}
		}
	}
}
