package peripherals

import (
	"context"
	"net"
	"strconv"
	"testing"
)

func TestTCPAdapterCombinesHostAndPortFields(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			close(accepted)
			_ = conn.Close()
		}
	}()
	handle, err := NewTCPAdapter("power").Open(context.Background(), Endpoint{Address: "127.0.0.1", Port: strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = handle.Close()
	<-accepted
}
