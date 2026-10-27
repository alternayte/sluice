package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// TestFetchBlocksInternalAddresses pins the addresses that fetch_url must never reach: the
// server would otherwise read its own network for the model.
func TestFetchBlocksInternalAddresses(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "::1", "fe80::1", "fd00:ec2::254", "::ffff:127.0.0.1", "::ffff:169.254.169.254", "2002:7f00:1::", "224.0.0.1"} {
		if !blockedAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s is not blocked", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s is blocked, want allowed", a)
		}
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer page.Close()
	s := &Service{}
	if _, err := s.fetch(context.Background(), page.URL, nil); !errors.Is(err, errBlockedAddress) {
		t.Fatalf("fetch of a loopback server: %v, want the blocked address error", err)
	}
	// localhost resolves to loopback after DNS.
	if _, err := s.fetch(context.Background(), strings.Replace(page.URL, "127.0.0.1", "localhost", 1), nil); !errors.Is(err, errBlockedAddress) {
		t.Fatalf("fetch of localhost: %v, want the blocked address error", err)
	}
	if _, err := s.fetch(context.Background(), "http://169.254.169.254/latest/meta-data/", nil); !errors.Is(err, errBlockedAddress) {
		t.Fatalf("fetch of the metadata address: %v, want the blocked address error", err)
	}
	if _, err := s.fetch(context.Background(), "file:///etc/passwd", nil); err == nil {
		t.Fatal("fetch of a file URL succeeded")
	}
	if _, err := s.fetch(context.Background(), "https://example.org/", []string{"docs.example.com"}); err == nil ||
		!strings.Contains(err.Error(), "not in the allowed domains") {
		t.Fatalf("fetch outside the allowed domains: %v", err)
	}
}

func TestDomainAllowed(t *testing.T) {
	domains := []string{"example.com"}
	for host, want := range map[string]bool{"example.com": true, "docs.example.com": true, "EXAMPLE.COM.": true,
		"badexample.com": false, "example.com.evil.net": false} {
		if got := domainAllowed(host, domains); got != want {
			t.Errorf("domainAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	if _, err := cleanDomains([]string{"https://example.com/x"}); err == nil {
		t.Error("cleanDomains accepted a URL")
	}
}

func TestHTMLText(t *testing.T) {
	got := htmlText(`<html><head><title>Docs</title><style>p{}</style></head><body><h1>Install</h1>` +
		`<p>Run   <code>sluice</code>.</p><script>alert(1)</script><ul><li>one</li><li>two</li></ul></body></html>`)
	want := "Title: Docs\n\nInstall\n\nRun sluice.\n\none\n\ntwo"
	if got != want {
		t.Fatalf("htmlText:\n%q\nwant\n%q", got, want)
	}
}
