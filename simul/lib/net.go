package lib

import (
	"net"
	"strconv"
	"time"
)

var afterPort = 11000 // Keeps the last port allocated

// GetFreeTCPPort returns a free tcp port or panics
func GetFreeTCPPort() int {
	for i := afterPort + 1; i < afterPort+1000; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+strconv.Itoa(i))
		if err != nil {
			continue
		}
		sock, err := net.ListenTCP("tcp", addr)
		if err != nil {
			continue
		}
		sock.Close()
		time.Sleep(2 * time.Millisecond)
		afterPort = i
		return afterPort
	}
	panic("free TCP port not found")
}

// GetFreeUDPPort returns a free usable UDP address
// We need to keep an history of the previous port we
//  allocated, we do this with this global variable.
func GetFreeUDPPort() int {
	for i := afterPort + 1; i < afterPort+1000; i++ {
		udpAddr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:"+strconv.Itoa(i))
		if err != nil {
			continue
		}
		sock, err := net.ListenUDP("udp4", udpAddr)
		if err != nil {
			continue
		}
		sock.Close()
		time.Sleep(2 * time.Millisecond)
		afterPort = i
		return afterPort
	}
	panic("free UDP port not found")
}
