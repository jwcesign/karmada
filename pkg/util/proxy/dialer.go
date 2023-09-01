package proxy

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
)

type httpConnectProxier struct {
	conn         net.Conn
	proxyAddress string
}

func (t *httpConnectProxier) proxy(ctx context.Context, addr string) (net.Conn, error) {
	return tunnelHTTPConnect(t.conn, t.proxyAddress, addr)
}

func tunnelHTTPConnect(proxyConn net.Conn, proxyAddress, addr string) (net.Conn, error) {
	fmt.Fprintf(proxyConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, "127.0.0.1")
	br := bufio.NewReader(proxyConn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("reading HTTP response from CONNECT to %s via proxy %s failed: %v",
			addr, proxyAddress, err)
	}
	if res.StatusCode != 200 {
		proxyConn.Close()
		return nil, fmt.Errorf("proxy error from %s while dialing %s, code %d: %v",
			proxyAddress, addr, res.StatusCode, res.Status)
	}

	// It's safe to discard the bufio.Reader here and return the
	// original TCP conn directly because we only use this for
	// TLS, and in TLS the client speaks first, so we know there's
	// no unbuffered data. But we can double-check.
	if br.Buffered() > 0 {
		proxyConn.Close()
		return nil, fmt.Errorf("unexpected %d bytes of buffered data from CONNECT proxy %q",
			br.Buffered(), proxyAddress)
	}
	return proxyConn, nil
}
