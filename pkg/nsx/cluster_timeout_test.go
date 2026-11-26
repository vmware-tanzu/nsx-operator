/* Copyright © 2026 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClusterTLSHandshakeTimeoutClosesConnections(t *testing.T) {
	for _, clientType := range []string{"normal", "no-balancer"} {
		for _, insecure := range []bool{false, true} {
			name := clientType + "/verified"
			if insecure {
				name = clientType + "/insecure"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = listener.Close() })

				type peer struct {
					conn   net.Conn
					closed chan struct{}
				}
				peers := make(chan peer, 3)
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						p := peer{conn: conn, closed: make(chan struct{})}
						peers <- p
						go func() {
							// Consume ClientHello but never send a ServerHello.
							_, _ = io.Copy(io.Discard, p.conn)
							close(p.closed)
						}()
					}
				}()

				cluster := &Cluster{config: &Config{HTTPTimeout: 1, Insecure: insecure}}
				client := cluster.createNoBalancerClient(1, 1)
				if clientType == "normal" {
					// Exercise the normal transport's actual connection establishment
					// without endpoint selection or retry obscuring the timeout.
					client = &http.Client{Transport: cluster.createTransport(1).Base, Timeout: time.Second}
				}
				t.Cleanup(client.CloseIdleConnections)

				var accepted []peer
				for i := 0; i < 3; i++ {
					resp, err := client.Get("https://" + listener.Addr().String() + "/api/v1/reverse-proxy/node/health")
					if resp != nil {
						_ = resp.Body.Close()
					}
					var timeout net.Error
					require.ErrorAs(t, err, &timeout)
					require.True(t, timeout.Timeout(), "request must fail because the TLS handshake timed out: %v", err)
					select {
					case p := <-peers:
						accepted = append(accepted, p)
						t.Cleanup(func() { _ = p.conn.Close() })
					case <-time.After(time.Second):
						t.Fatal("request did not reach the stalled TLS peer")
					}
				}

				// A request timeout alone does not cancel Transport's detached dial.
				// Observe the server-side socket closing, rather than just the error.
				deadline := time.NewTimer(500 * time.Millisecond)
				defer deadline.Stop()
				for _, p := range accepted {
					select {
					case <-p.closed:
					case <-deadline.C:
						t.Fatal("TLS socket remained open after the connection deadline")
					}
				}
				t.Log("all 3 stalled TLS sockets closed after their connection deadlines")
			})
		}
	}
}
