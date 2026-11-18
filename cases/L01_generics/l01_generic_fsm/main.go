// L01: 泛型状态机：S/E 双类型参数 + 泛型负载，转移表/历史/拒绝路径与独立模型逐步比对。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type pcell struct {
	id   int64
	name string
	blob []byte
}

type stateID int

const (
	stNew stateID = iota
	stReady
	stRunning
	stPaused
	stStopped
)

type eventID int

const (
	evStart eventID = iota
	evPause
	evResume
	evStop
	evReset
)

func (e eventID) String() string {
	switch e {
	case evStart:
		return "start"
	case evPause:
		return "pause"
	case evResume:
		return "resume"
	case evStop:
		return "stop"
	default:
		return "reset"
	}
}

func (s stateID) String() string {
	switch s {
	case stNew:
		return "new"
	case stReady:
		return "ready"
	case stRunning:
		return "running"
	case stPaused:
		return "paused"
	default:
		return "stopped"
	}
}

type step[S comparable, E comparable] struct {
	from, to S
	ev       E
	ok       bool
}

type Machine[S comparable, E comparable, T any] struct {
	state    S
	trans    map[S]map[E]S
	payload  *T
	hist     []step[S, E]
	rejected int
}

func NewMachine[S comparable, E comparable, T any](initial S, p *T) *Machine[S, E, T] {
	return &Machine[S, E, T]{state: initial, trans: make(map[S]map[E]S), payload: p}
}

func (m *Machine[S, E, T]) Add(from S, ev E, to S) bool {
	row := m.trans[from]
	if row == nil {
		row = make(map[E]S)
		m.trans[from] = row
	}
	if _, dup := row[ev]; dup {
		return false
	}
	row[ev] = to
	return true
}

func (m *Machine[S, E, T]) Fire(ev E) bool {
	row := m.trans[m.state]
	to, ok := row[ev]
	if !ok {
		m.rejected++
		m.hist = append(m.hist, step[S, E]{from: m.state, to: m.state, ev: ev})
		return false
	}
	from := m.state
	m.state = to
	m.hist = append(m.hist, step[S, E]{from: from, to: to, ev: ev, ok: true})
	return true
}

func (m *Machine[S, E, T]) State() S              { return m.state }
func (m *Machine[S, E, T]) Rejected() int         { return m.rejected }
func (m *Machine[S, E, T]) History() []step[S, E] { return m.hist }

type transRow struct {
	from stateID
	ev   eventID
	to   stateID
}

// model 是独立的引用状态机，转移表由同一份行数据构建但推进逻辑分离。
type model struct {
	state    stateID
	trans    map[stateID]map[eventID]stateID
	rejected int
	accepted int
	hist     []step[stateID, eventID]
}

func newModel() *model {
	return &model{state: stNew, trans: make(map[stateID]map[eventID]stateID)}
}

func (mo *model) add(from stateID, ev eventID, to stateID) {
	if mo.trans[from] == nil {
		mo.trans[from] = make(map[eventID]stateID)
	}
	mo.trans[from][ev] = to
}

func (mo *model) fire(ev eventID) bool {
	to, ok := mo.trans[mo.state][ev]
	if !ok {
		mo.rejected++
		mo.hist = append(mo.hist, step[stateID, eventID]{from: mo.state, to: mo.state, ev: ev})
		return false
	}
	from := mo.state
	mo.state = to
	mo.accepted++
	mo.hist = append(mo.hist, step[stateID, eventID]{from: from, to: to, ev: ev, ok: true})
	return true
}

var (
	failed   int32
	gcSink   [][]byte
	cellSink []*pcell
	uafSeed  int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

func main() {
	uafSeed = envSeed()
	n := 160 + int(uafSeed%40)

	cell := &pcell{id: 1, name: "fsm", blob: make([]byte, 32)}
	cell.blob[0] = 0x7a
	cellSink = []*pcell{cell}

	table := []transRow{
		{stNew, evStart, stReady},
		{stNew, evReset, stNew},
		{stReady, evStart, stRunning},
		{stReady, evStop, stStopped},
		{stReady, evReset, stNew},
		{stRunning, evPause, stPaused},
		{stRunning, evStop, stStopped},
		{stRunning, evReset, stNew},
		{stPaused, evResume, stRunning},
		{stPaused, evStop, stStopped},
		{stPaused, evReset, stNew},
		{stStopped, evReset, stNew},
	}

	m := NewMachine[stateID, eventID, pcell](stNew, cell)
	mo := newModel()
	for _, tr := range table {
		ck(m.Add(tr.from, tr.ev, tr.to), "duplicate transition rejected unexpectedly")
		mo.add(tr.from, tr.ev, tr.to)
	}
	ck(!m.Add(stNew, evStart, stRunning), "duplicate transition accepted")

	// 固定先走一条非法转移，确保拒绝路径被覆盖。
	ck(!m.Fire(evPause), "pause from new should be rejected")
	ck(!mo.fire(evPause), "model pause from new should be rejected")
	ck(m.State() == stNew, "rejected event changed state")

	state := uint64(uafSeed)*0x2545f4914f6cdd1d + 0x9e37
	for i := 0; i < n; i++ {
		ev := eventID(nextRand(&state) % 5)
		got := m.Fire(ev)
		want := mo.fire(ev)
		ck(got == want, fmt.Sprintf("step=%d event=%s accept mismatch", i, ev))
		ck(m.State() == mo.state, fmt.Sprintf("step=%d event=%s state got=%s want=%s", i, ev, m.State(), mo.state))
		ck(m.Rejected() == mo.rejected, fmt.Sprintf("step=%d rejected got=%d want=%d", i, m.Rejected(), mo.rejected))
		ck(len(m.History()) == len(mo.hist), fmt.Sprintf("step=%d history length mismatch", i))
		if i%32 == 31 {
			gcStress()
			ck(m.payload == cell, "fsm payload pointer identity changed after GC")
			ck(m.payload.id == 1 && m.payload.name == "fsm", "fsm payload corrupted after GC")
			ck(m.payload.blob[0] == 0x7a, "fsm payload blob corrupted after GC")
		}
	}

	hist := m.History()
	ck(len(hist) == n+1, fmt.Sprintf("history len got=%d want=%d", len(hist), n+1))
	ck(hist[0].from == stNew && !hist[0].ok, "first rejected step corrupted")
	prev := hist[0].to
	for i := 1; i < len(hist); i++ {
		ck(hist[i].from == prev, fmt.Sprintf("history chain broken at %d", i))
		ck(hist[i].ok == mo.hist[i].ok, "history ok flag diverged")
		ck(hist[i].ev == mo.hist[i].ev, "history event diverged")
		ck(hist[i].to == mo.hist[i].to, "history target diverged")
		prev = hist[i].to
	}
	ck(m.Rejected() > 0, "random walk produced no rejected event")
	gcStress()
	ck(m.State() == mo.state, "final state changed after GC")

	// 第二次实例化 Machine[string,string,pcell]：字符串状态/事件。
	sm := NewMachine[string, string, pcell]("new", cell)
	ck(sm.Add("new", "go", "ready"), "string add failed")
	ck(sm.Add("ready", "go", "running"), "string add failed")
	ck(sm.Add("running", "back", "ready"), "string add failed")
	ck(sm.Fire("go") && sm.State() == "ready", "string fsm first transition failed")
	ck(sm.Fire("go") && sm.State() == "running", "string fsm second transition failed")
	ck(!sm.Fire("go"), "string fsm accepted undefined event")
	ck(sm.Fire("back") && sm.State() == "ready", "string fsm back transition failed")
	gcStress()
	ck(sm.payload == cell && sm.State() == "ready", "string fsm payload/state corrupted after GC")
	ck(sm.Rejected() == 1, "string fsm rejected count corrupted")

	finish("l01_generic_fsm")
}
