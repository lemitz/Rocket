package discovery

import (
	"net"
	"testing"
	"time"
)

func TestDiscoveryRespondsOnlyToRocket(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go Serve(server, "0123456789abcdef0123456789abcdef", "PC-ONE")
	client, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	if _, err := client.Write([]byte(Request)); err != nil {
		t.Fatal(err)
	}
	var buf [256]byte
	n, err := client.Read(buf[:])
	if err != nil || string(buf[:n]) != Response+"|0123456789abcdef0123456789abcdef|PC-ONE" {
		t.Fatalf("discovery: %q %v", buf[:n], err)
	}
}
