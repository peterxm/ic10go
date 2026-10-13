package testbench

import (
	"bufio"
	"encoding/json"
	"math"
	"net"
	"strings"
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
	paused bool
	// compileErr, when set, is echoed as the push result's `compileError`.
	// The mod answers ok:true even then (the chip keeps its old program).
	compileErr any
	// runErr, when set, is echoed as the run result's `error` (a chip pinned on
	// an error line).
	runErr any
	// pauseHonoured makes `pause` report the requested state; when false it
	// reports the world as still running (a multiplayer client).
	pauseHonoured bool
	// sendStray makes the server emit an id-less error reply right after the
	// hello (what the mod does for a request line it cannot parse).
	sendStray bool
	// setErr, when set, makes `set` answer with that protocol error.
	setErr any
}

func startFake(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeServer{ln: ln, values: map[string]float64{}, conns: map[net.Conn]bool{}, pauseHonoured: true, paused: true}
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
	f.mu.Lock()
	stray := f.sendStray
	f.mu.Unlock()
	if stray {
		send(map[string]any{"id": nil, "ok": false, "error": map[string]any{
			"code": "bad-request", "message": "malformed request line",
		}})
	}

	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var req fakeReq
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req.Cmd {
		case "ping":
			f.mu.Lock()
			paused := f.pauseHonoured || f.paused
			f.mu.Unlock()
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"mod": "ic10go-testbench", "version": "0.1.0", "gameVersion": "test", "paused": paused, "chips": 1,
			}})
		case "pause":
			var a struct {
				On bool `json:"on"`
			}
			json.Unmarshal(req.Args, &a)
			f.mu.Lock()
			f.paused = a.On && f.pauseHonoured
			got := f.paused
			f.mu.Unlock()
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{"paused": got, "requested": a.On}})
		case "chip.list":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"chips": []map[string]any{{
					"id": 1, "index": 0, "name": "TestChip",
					"fp":  "1a2b3c4d",
					"pos": map[string]any{"x": 12.5, "y": 3.0, "z": -7.25, "yaw": 90.0},
				}},
			}})
		case "push":
			f.mu.Lock()
			ce := f.compileErr
			f.mu.Unlock()
			res := map[string]any{
				"chip": map[string]any{"name": "TestChip"}, "lines": 3,
			}
			if ce != nil {
				res["compileError"] = ce
			}
			send(map[string]any{"id": req.ID, "ok": true, "result": res})
		case "set":
			f.mu.Lock()
			se := f.setErr
			f.mu.Unlock()
			if se != nil {
				send(map[string]any{"id": req.ID, "ok": false, "error": se})
				break
			}
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
			f.mu.Lock()
			re := f.runErr
			f.mu.Unlock()
			res := map[string]any{"ticks": 1, "line": 1}
			if re != nil {
				res["error"] = re
			}
			send(map[string]any{"id": req.ID, "ok": true, "result": res})
		case "state":
			send(map[string]any{"id": req.ID, "ok": true, "result": map[string]any{
				"chip": map[string]any{"name": "TestChip"},
				// r5 is the protocol's null ("never written"/quiet NaN), and r6/r7
				// are the game serializer's non-finite spellings.
				"registers": map[string]any{"r0": 105, "sp": 4, "r5": nil, "r6": "NaN", "r7": "Infinity"},
			}})
		case "hud":
			var a map[string]any
			json.Unmarshal(req.Args, &a)
			res := map[string]any{
				"on":     true,
				"player": map[string]any{"x": 1.0, "y": 2.0, "z": 3.0, "yaw": 90.0, "pitch": -5.0},
			}
			if a["target"] != nil {
				res["target"] = map[string]any{"x": 669.0, "y": 192.0, "z": -627.0, "label": "118"}
			}
			send(map[string]any{"id": req.ID, "ok": true, "result": res})
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
	if chips[0].Fingerprint != "1a2b3c4d" {
		t.Errorf("fingerprint = %q, want 1a2b3c4d", chips[0].Fingerprint)
	}
	if chips[0].Pos == nil || chips[0].Pos.X != 12.5 || chips[0].Pos.Z != -7.25 {
		t.Errorf("pos = %+v, want {12.5 3 -7.25}", chips[0].Pos)
	}
}

func TestClientHud(t *testing.T) {
	c, _ := dialFake(t)
	st, err := c.Hud(nil)
	if err != nil {
		t.Fatalf("hud: %v", err)
	}
	if !st.On || st.Player == nil || st.Player.Yaw != 90 || st.Player.Pitch != -5 {
		t.Fatalf("hud state: %+v", st)
	}
	st, err = c.Hud(map[string]any{"target": map[string]any{"x": 669.0, "y": 192.0, "z": -627.0}})
	if err != nil {
		t.Fatalf("hud target: %v", err)
	}
	if st.Target == nil || st.Target.X != 669 || st.Target.Label != "118" {
		t.Fatalf("hud target: %+v", st.Target)
	}
}

func TestFingerprint(t *testing.T) {
	// CRLF and trailing whitespace must not change the fingerprint, and the
	// value must be stable across the mod/client (locked to the sha1 prefix).
	base := "move r0 1\nmove r1 2"
	fp := Fingerprint(base)
	if fp != "2092a44b" {
		t.Fatalf("fingerprint = %q, want 2092a44b", fp)
	}
	for _, variant := range []string{
		base + "\n",
		"move r0 1\r\nmove r1 2\r\n",
		base + "\n\n  ",
	} {
		if got := Fingerprint(variant); got != fp {
			t.Errorf("fingerprint(%q) = %q, want %q", variant, got, fp)
		}
	}
	if Fingerprint("move r0 1") == Fingerprint("move r0 2") {
		t.Error("different sources share a fingerprint")
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
	if v := st.Registers["r0"]; v == nil || float64(*v) != 105 {
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

// A push that did not compile must fail loudly (review B17): the mod answers
// ok:true and leaves the chip running its previous program, so a caller that
// ignores compileError reports a green scenario against the wrong binary.
func TestPushCompileErrorIsFatal(t *testing.T) {
	c, f := dialFake(t)
	f.mu.Lock()
	f.compileErr = map[string]any{
		"code":        "在第 2 行出现 <color=red>UnrecognisedInstruction</color> 错误\n",
		"compilation": true, "line": 1,
	}
	f.mu.Unlock()

	res, err := c.Push("yield", nil, nil)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.CompileError == nil || !res.CompileError.Compilation || res.CompileError.Line != 1 {
		t.Fatalf("compileError not decoded: %+v", res.CompileError)
	}
	// The message must be readable and free of the game's rich-text markup.
	msg := res.CompileError.Error()
	if !strings.Contains(msg, "UnrecognisedInstruction") || strings.Contains(msg, "<color") {
		t.Errorf("error text = %q", msg)
	}

	sc := &Scenario{Program: "x.icg", Cases: []Case{{Name: "ok"}}}
	if _, err := (&Runner{C: c, Code: "yield"}).Run(sc); err == nil {
		t.Fatal("a scenario whose push did not compile must fail, not pass")
	}
}

// A run that leaves the chip pinned on an error line must fail the case: the
// chip never advances, so the assertions would be meaningless (review B22).
func TestRunChipErrorFailsCase(t *testing.T) {
	c, f := dialFake(t)
	f.mu.Lock()
	f.runErr = map[string]any{
		"code":        "在第 3 行出现 <color=red>StackUnderFlow</color> 错误\n",
		"compilation": false, "line": 2,
	}
	f.mu.Unlock()

	rr, err := c.Run(2, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rr.Error == nil || !strings.Contains(rr.Error.Error(), "StackUnderFlow") || rr.Error.Line != 2 {
		t.Fatalf("run error not decoded: %+v", rr.Error)
	}

	sc := &Scenario{Program: "x.icg", Cases: []Case{
		{Name: "runs", Run: 2, Expect: map[string]float64{"d1.Setting": 10}},
	}}
	rep, err := (&Runner{C: c, Code: "yield"}).Run(sc)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	if rep.Failed != 1 || rep.Cases[0].Passed {
		t.Fatalf("a case whose chip is stuck must fail: %+v", rep)
	}
	if !strings.Contains(rep.Cases[0].Err, "stuck on an error") {
		t.Errorf("case error = %q", rep.Cases[0].Err)
	}
}

// A pause that does not take effect must be an error, not a silent green run
// (review B23): on a multiplayer client the world keeps ticking, so a scenario
// would be non-deterministic.
func TestPauseForRunNotHonoured(t *testing.T) {
	c, f := dialFake(t)
	if _, err := c.PauseForRun(); err != nil {
		t.Fatalf("an already-paused world should be fine: %v", err)
	}

	f.mu.Lock()
	f.paused = false
	f.pauseHonoured = false
	f.mu.Unlock()

	resume, err := c.PauseForRun()
	if err == nil {
		t.Fatal("a pause that was not honoured must be an error")
	}
	if resume == nil {
		t.Fatal("resume must not be nil even on error")
	}
	if rerr := resume(); rerr != nil {
		t.Errorf("resume after a failed pause should be a no-op: %v", rerr)
	}
}

// A protocol null means "never written" (quiet NaN) and is NOT 0, and the
// game's serializer also spells the non-finite values as strings; neither may
// silently become 0 (review B21/P1).
func TestStateKeepsNullDistinctFromZero(t *testing.T) {
	c, _ := dialFake(t)
	st, err := c.State([]string{"registers"}, false, nil)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if v := st.Registers["r0"]; v == nil || float64(*v) != 105 {
		t.Fatalf("r0 = %v", v)
	}
	if v, ok := st.Registers["r5"]; !ok || v != nil {
		t.Errorf("a null register must stay nil (present=%v), got %v", ok, v)
	}
	if v := st.Registers["r6"]; v == nil || !math.IsNaN(float64(*v)) {
		t.Errorf(`the "NaN" spelling must decode to NaN, got %v`, v)
	}
	if v := st.Registers["r7"]; v == nil || !math.IsInf(float64(*v), 1) {
		t.Errorf(`the "Infinity" spelling must decode to +Inf, got %v`, v)
	}

	// An expectation of 0 must NOT be satisfied by a null register.
	sc := &Scenario{Program: "x.icg", Cases: []Case{
		{Name: "null is not zero", ExpectReg: map[string]float64{"r5": 0}},
	}}
	rep, err := (&Runner{C: c, Code: "yield"}).Run(sc)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	if rep.Passed != 0 || rep.Failed != 1 {
		t.Fatalf("expectReg 0 on a null register must fail: %+v", rep)
	}

	// Re-exporting must stay valid JSON (NaN/Inf are not JSON numbers).
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(b) {
		t.Fatalf("state JSON is invalid: %s", b)
	}
}

// A reply the client cannot match to a request must be surfaced rather than
// dropped, or the caller waits out the full timeout with no reason (review B30).
func TestStrayErrorResponseIsSurfaced(t *testing.T) {
	f := startFake(t)
	f.mu.Lock()
	f.sendStray = true
	f.mu.Unlock()
	c, err := Dial(f.addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e := c.StrayError(); e != nil {
			if !strings.Contains(e.Error(), "malformed request line") {
				t.Errorf("stray error = %v", e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the id-less error response was dropped")
}

// A request that cannot even be encoded must not leak its pending slot
// (review B32).
func TestEncodeFailureDoesNotLeakPending(t *testing.T) {
	c, _ := dialFake(t)
	if _, err := c.Call("set", map[string]any{"n": math.NaN()}); err == nil {
		t.Fatal("encoding NaN should fail")
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending leaked: %d", n)
	}
}

// An environment failure (the rig) must be reported apart from a failing
// assertion (the program), so CI can tell them apart (review B28).
func TestEnvFailureIsNotAnAssertionFailure(t *testing.T) {
	c, f := dialFake(t)
	f.mu.Lock()
	f.setErr = map[string]any{"code": "no-chip", "message": "no chip selected"}
	f.mu.Unlock()

	sc := &Scenario{Program: "x.icg", Cases: []Case{
		{Name: "rig is broken", Set: map[string]float64{"d1.Setting": 10}},
		{Name: "assertion fails", Expect: map[string]float64{"d1.Setting": 999}},
	}}
	rep, err := (&Runner{C: c, Code: "yield"}).Run(sc)
	if err != nil {
		t.Fatalf("scenario: %v", err)
	}
	if rep.EnvFailed != 1 || rep.Failed != 1 {
		t.Fatalf("want 1 env + 1 assertion failure, got %+v", rep)
	}
	if !rep.Cases[0].Env || rep.Cases[1].Env {
		t.Errorf("misclassified: %+v", rep.Cases)
	}
	if rep.OK() {
		t.Error("a report with an environment failure is not OK")
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
