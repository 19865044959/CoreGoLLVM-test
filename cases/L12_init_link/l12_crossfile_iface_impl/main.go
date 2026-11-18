// L12: cross-file interface implementation. The interfaces live in one file,
// tcpTransport's methods are split across two files, and udpTransport and the
// typed-nil implementation live in others. Covers embedding, interface
// equality, type assertions, typed nil, method expressions and a global
// interface registry under GC.
// P2: TORTURE_ROUNDS scales the dispatch loop.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 32 {
		v = 32
	}
	return v
}

func catchPanic(fn func()) (recovered bool) {
	defer func() {
		if recover() != nil {
			recovered = true
		}
	}()
	fn()
	return false
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	ck(joinedNames() == "nil,tcp,udp", "global interface registry contents")
	tcp := impls["tcp"]
	udp := impls["udp"]
	nilIface := impls["nil"]
	ck(tcp != nil && udp != nil, "concrete transports present")
	ck(tcp == impls["tcp"], "same pointer compares equal through interfaces")
	ck(tcp != udp, "different implementations compare unequal")
	ck(nilIface != nil, "typed-nil interface is not nil")

	// TransportCloser embedding: union method set.
	tc, ok := tcp.(TransportCloser)
	ck(ok && tc != nil, "tcp implements embedded TransportCloser")
	ck(tc.Send([]byte("abcde")) == 5, "tcp send length")
	ck(tc.Stats() == (Stats{Sent: 5, Failed: 0}), "tcp stats after send")
	ck(tc.Close() == nil, "tcp first close")
	ck(tc.Close() == errClosed, "tcp second close returns sentinel")
	ck(tc.Send([]byte("x")) == -1, "send after close fails")
	ck(tc.Stats() == (Stats{Sent: 5, Failed: 1}), "failed send is counted")

	uc := udp.(TransportCloser)
	ck(uc.Send([]byte{1, 2, 3}) == 3, "udp send length")
	ck(uc.Close() == nil && uc.Close() == errClosed, "udp close semantics")
	ck(uc.Stats() == (Stats{Sent: 3, Failed: 0}), "udp stats")

	// Typed nil dynamic value with a non-nil interface.
	p, ok := nilIface.(*nilTransport)
	ck(ok && p == nil, "typed nil assertion yields a nil pointer")
	ck(nilIface.Name() == "nil", "method on typed nil receiver")
	ck(nilIface.Stats() == (Stats{}), "Stats on typed nil receiver")
	ck(nilIface.(Closer).Close() == nil, "Close on typed nil receiver")
	ck(catchPanic(func() { nilIface.Send(nil) }), "Send on typed nil panics")
	ck(nilIface == Transport((*nilTransport)(nil)), "typed nil interface equality")
	_, ok = nilIface.(Closer)
	ck(ok, "typed nil still satisfies Closer")

	// Interface conversion and negative assertions.
	n := udp.(named)
	ck(n.Name() == "udp", "interface-to-interface conversion")
	_, ok = udp.(interface{ Missing() string })
	ck(!ok, "missing method assertion fails")

	// Method values and method expressions.
	nameFn := tcp.Name
	ck(nameFn() == "tcp", "method value through interface")
	exprName := Transport.Name
	ck(exprName(udp) == "udp", "method expression on interface")
	exprStats := Transport.Stats
	ck(exprStats(udp).Sent == 3, "Stats method expression")

	// Type switch dispatch: concrete cases win over the interface case.
	ck(dispatch(tcp) == "concrete-tcp:tcp", "type switch concrete tcp")
	ck(dispatch(udp) == "concrete-udp:udp", "type switch concrete udp")
	ck(dispatch(nilIface) == "iface:nil", "type switch interface fallback")
	ck(dispatch(42) == "unknown", "type switch default")

	// TORTURE-driven dispatch loop, rotated by seed.
	iters := 2000 * rounds
	if iters > 32000 {
		iters = 32000
	}
	names := implNames()
	start := int(seedVal % 3)
	for i := 0; i < iters; i++ {
		name := names[(start+i)%len(names)]
		if got := dispatch(impls[name]); got != expectedDispatch(name) {
			ck(false, "dispatch result in torture loop")
			break
		}
	}

	// Concurrent read-only interface dispatch + GC.
	const workers = 8
	perWorker := 100 * rounds
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := names[(start+w+i)%len(names)]
				v := impls[name]
				if v == nil || v.Name() != name {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = v.Stats()
			}
		}(w)
	}
	for i := 0; i < 6; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent interface dispatch under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(impls["tcp"] == tcp && impls["udp"] == udp && impls["nil"] == nilIface,
		"registry interface values survive GC")
	ck(tcp.(Transport).Stats() == (Stats{Sent: 5, Failed: 1}), "tcp stats survive GC")
	ck(tcp.(*tcpTransport).data[31] == byte(1+31), "tcp payload survives GC")
	ck(nilIface != nil && nilIface.Name() == "nil", "typed nil interface survives GC")

	finish("l12_crossfile_iface_impl")
}
