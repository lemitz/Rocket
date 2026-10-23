package discovery

import (
	"bytes"
	"net"
	"strings"
)

const Request = "ROCKET_DISCOVER_V1"
const Response = "ROCKET_HERE_V1"
const Port = 42069

// Serve responds only to an exact, short probe. ID identifies a PC in the UI,
// but discovery does not authenticate peers.
func Serve(conn *net.UDPConn, id, name string) {
	name = strings.ReplaceAll(name, "|", "_")
	if chars := []rune(name); len(chars) > 80 {
		name = string(chars[:80])
	}
	reply := []byte(Response + "|" + id + "|" + name)
	var buf [512]byte
	for {
		n, remote, err := conn.ReadFromUDP(buf[:])
		if err != nil {
			return
		}
		if (remote.IP.IsPrivate() || remote.IP.IsLoopback() || remote.IP.IsLinkLocalUnicast()) && bytes.Equal(buf[:n], []byte(Request)) {
			_, _ = conn.WriteToUDP(reply, remote)
		}
	}
}
