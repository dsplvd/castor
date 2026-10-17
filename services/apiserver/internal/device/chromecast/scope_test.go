package chromecast

import (
	castdns "github.com/vishen/go-chromecast/dns"
	"net"
	"testing"
)

func TestLinkLocalDiscoveryPreservesInterface(t *testing.T) {
	entry := castdns.CastEntry{AddrV6: net.ParseIP("fe80::123"), Port: 8009}
	got, ok := infoOnInterface(entry, "en7")
	if !ok || got.Address != "fe80::123%en7" {
		t.Fatalf("info=%+v,%v", got, ok)
	}
	if _, ok := infoOnInterface(entry, ""); ok {
		t.Fatal("unscoped link-local address accepted")
	}
	entry.AddrV4 = net.ParseIP("192.0.2.1")
	got, ok = infoOnInterface(entry, "en7")
	if !ok || got.Address != "192.0.2.1" {
		t.Fatalf("IPv4 info=%+v,%v", got, ok)
	}
}
