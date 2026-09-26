package testbench

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeServer is an in-process stand-in for the in-game mod. It mirrors the
// protocol in docs/ingame-testbench.md closely enough to exercise the client.
type fakeServer struct {
	ln net.Listener

	mu     sync.Mutex
	values map[string]float64 // "d1.Setting" -> value
	conns  map[net.Conn]bool
}

func startFake(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeServer{ln: ln, values: map[string]float64{}, conns: map[net.Conn]bool{}}
	go f.accept()
	t.Cleanup(func() { f.stop() })
	return f
}

func (f *fakeServer) addr() string { return f.ln.Addr().String() }

// stop closes the listener and every live connection, simulating the game
// going away.
func (f *fakeServer) stop() {
	f.ln.Close()
	f.mu.Lock()
	for c := range f.conns {
		c.Close()
	}
	f.conns = map[net.Conn]bool{}
	f.mu.Unlock()
}

func (f *fakeServer) accept() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns[conn] = true
		f.mu.Unlock()
		go f.serve(conn)
	}
}

type fakeReq struct {
	ID   int             `json:"id"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args"`
}

func (f *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	defer func() {
		f.mu.Lock()
		delete(f.conns, conn)
		f.mu.Unlock()
	}()
	w := bufio.NewWriter(conn)
	send := func(v any) {
		b, _ := json.Marshal(v)
		w.Write(b)
		w.WriteByte('\n')
		w.Flush()
	}
	send(map[string]any{"event": "hello", "mod": "ic10go-testbench", "version": "0.1.0"})

	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var req fakeReq
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req.Cmd {
		case "ping":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"mod": "ic10go-testbench", "version": "0.1.0", "gameVersion": "test", "paused": true, "chips": 1,
			}})
		case "chip.list":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"chips": []map[string]any{{"id": 1, "index": 0, "name": "TestChip"}},
			}})
		case "push":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"chip": map[string]any{"name": "TestChip"}, "lines": 3,
			}})
		case "set":
			var a struct {
				Writes []DeviceWrite `json:"writes"`
			}
			json.Unmarshal(req.Args, &a)
			f.mu.Lock()
			for _, wr := range a.Writes {
				f.values[wr.Port+"."+wr.Logic] = wr.Value
			}
			f.mu.Unlock()
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{"applied": len(a.Writes)}})
		case "get":
			var a struct {
				Reads []DeviceRead `json:"reads"`
			}
			json.Unmarshal(req.Args, &a)
			f.mu.Lock()
			vals := make([]map[string]any, 0, len(a.Reads))
			for _, rd := range a.Reads {
				v := f.values[rd.Port+"."+rd.Logic]
				vals = append(vals, map[string]any{"port": rd.Port, "logic": rd.Logic, "value": v})
			}
			f.mu.Unlock()
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{"values": vals}})
		case "run":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{"ticks": 1, "line": 1}})
		case "state":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"chip":      map[string]any{"name": "TestChip"},
				"registers": map[string]any{"r0": 105, "sp": 4},
			}})
		default:
			send(map[string]any{"id": req.ID, "ok": false, "error": map[string]any{"code": "bad-request", "message": "unknown " + req.Cmd}})
		}
	}
}

func dialFake(t *testing.T) (*Client, *fakeServer) {
	t.Helper()
	f := startFake(t)
	c, err := Dial(f.addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, f
}

func TestClientPingAndChips(t *testing.T) {
	c, _ := dialFake(t)

	h, err := c.Ping()
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if h.GameVersion != "test" || h.Chips != 1 {
		t.Fatalf("unexpected hello: %+v", h)
	}

	chips, err := c.ListChips()
	if err != nil {
		t.Fatalf("chips: %v", err)
	}
	if len(chips) != 1 || chips[0].Name != "TestChip" {
		t.Fatalf("unexpected chips: %+v", chips)
	}
}

func TestClientSetGetRunState(t *testing.T) {
	c, _ := dialFake(t)

	n, err := c.SetWrites([]DeviceWrite{{Port: "d1", Logic: "Setting", Value: 10}}, nil, false)
	if err != nil || n != 1 {
		t.Fatalf("set: n=%d err=%v", n, err)
	}
	got, err := c.GetValues([]DeviceRead{{Port: "d1", Logic: "Setting"}}, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["d1.Setting"] != 10 {
		t.Fatalf("got %v", got)
	}

	if _, err := c.Run(5, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	st, err := c.State([]string{"registers"}, false, nil)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.Registers["r0"] != 105 {
		t.Fatalf("registers: %+v", st.Registers)
	}
}

func TestScenarioRun(t *testing.T) {
	c, _ := dialFake(t)
	sc := &Scenario{
		Program: "x.icg",
		Ports:   map[string]string{"d0": "LED", "d1": "Dial"},
		Cases: []Case{
			{Name: "ok", Set: map[string]float64{"d1.Setting": 10}, Run: 5,
				Expect: map[string]float64{"d1.Setting": 10}, ExpectReg: map[string]float64{"r0": 105}},
			{Name: "fail", Expect: map[string]float64{"d1.Setting": 999}},
		},
	}
	rep, err := (&Runner{C: c, Code: "yield"}).Run(sc)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if rep.Passed != 1 || rep.Failed != 1 {
		t.Fatalf("report: %+v", rep)
	}
	if rep.Cases[0].Passed != true || rep.Cases[1].Passed != false {
		t.Fatalf("cases: %+v", rep.Cases)
	}
}

func TestSplitKey(t *testing.T) {
	for _, tc := range []struct {
		in, port, logic string
		wantErr         bool
	}{
		{"d1.Setting", "d1", "Setting", false},
		{"0.On", "d0", "On", false},
		{"d2.slot", "d2", "slot", false},
		{"noDot", "", "", true},
		{"d1.", "", "", true},
	} {
		port, logic, err := splitKey(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil || port != tc.port || logic != tc.logic {
			t.Errorf("%q: got %q/%q err=%v", tc.in, port, logic, err)
		}
	}
}

// Ensure a dead connection surfaces as an error rather than hanging.
func TestCallAfterServerClose(t *testing.T) {
	f := startFake(t)
	c, err := Dial(f.addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	f.stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Ping(); err != nil {
			return // expected
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected an error after the server closed")
}
