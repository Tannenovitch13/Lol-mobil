// Package netx holds network helpers shared by the companion's outgoing
// connections (build sites, MQTT brokers, relay).
package netx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Public DNS servers asked when the system resolver fails: some networks'
// resolvers time out or answer "no such host" for sites that exist.
var publicDNS = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

var dialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

var publicResolver = &net.Resolver{
	PreferGo: true,
	Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, server := range publicDNS {
			d := net.Dialer{Timeout: 2 * time.Second}
			c, err := d.DialContext(ctx, network, server)
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	},
}

// DialContext dials like net.Dialer, but when the host name cannot be
// resolved by the system it asks public DNS servers before giving up.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, network, addr)
	var dnsErr *net.DNSError
	if err == nil || !errors.As(err, &dnsErr) {
		return conn, err
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || net.ParseIP(host) != nil {
		return nil, err
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	ips, lookupErr := publicResolver.LookupIPAddr(lookupCtx, host)
	if lookupErr != nil || len(ips) == 0 {
		return nil, err
	}
	for _, ip := range ips {
		c, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return c, nil
		}
		err = dialErr
	}
	return nil, err
}

// Transport is an HTTP transport using DialContext.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = DialContext
	return t
}
