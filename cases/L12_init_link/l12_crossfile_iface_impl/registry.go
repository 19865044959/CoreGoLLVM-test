package main

import (
	"sort"
	"strings"
)

// impls is a global interface table built during package variable
// initialization. Values are interface-typed, including one typed nil.
var impls = buildImpls()

func buildImpls() map[string]Transport {
	m := make(map[string]Transport, 3)
	m["tcp"] = newTCP("tcp", 1)
	m["udp"] = newUDP("udp", 2)
	m["nil"] = (*nilTransport)(nil)
	return m
}

func implNames() []string {
	out := make([]string, 0, len(impls))
	for name := range impls {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func dispatch(v any) string {
	switch x := v.(type) {
	case *tcpTransport:
		return "concrete-tcp:" + x.Name()
	case *udpTransport:
		return "concrete-udp:" + x.Name()
	case Transport:
		return "iface:" + x.Name()
	default:
		return "unknown"
	}
}

func expectedDispatch(name string) string {
	switch name {
	case "tcp":
		return "concrete-tcp:tcp"
	case "udp":
		return "concrete-udp:udp"
	case "nil":
		return "iface:nil"
	}
	return "unknown"
}

func joinedNames() string { return strings.Join(implNames(), ",") }
