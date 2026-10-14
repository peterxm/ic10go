// Package testbench is the Go client for the ic10go in-game testbench mod.
//
// The mod listens on 127.0.0.1:7800 and speaks newline-delimited JSON (one JSON
// object per line). This package handles the request/response correlation and
// exposes the unsolicited `state` events used for live views.
//
// See docs/ingame-testbench.md for the protocol and command set.
package testbench

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DefaultAddr is the address the mod binds to unless configured otherwise.
const DefaultAddr = "127.0.0.1:7800"

// Addr returns the testbench address from IC10_BENCH_ADDR, or DefaultAddr.
func Addr() string {
	if v := os.Getenv("IC10_BENCH_ADDR"); v != "" {
		return v
	}
	return DefaultAddr
}

// Error is a protocol error carrying the stable, non-localized code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// Client is a connection to the in-game testbench.
type Client struct {
	conn net.Conn
	enc  *json.Encoder

	mu      sync.Mutex
	nextID  int
	pending map[int]chan reply
	// strayErr is the last error response that matched no pending request
	// (review B30).
	strayErr *Error

	events chan json.RawMessage

	closed chan struct{}

	closeOnce sync.Once
	closeErr  error
}

type reply struct {
	result json.RawMessage
	err    error
}

// Dial connects to the testbench at addr (host:port).
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to game testbench at %s: %w\n  is Stationeers running with the ic10go-testbench mod enabled?", addr, err)
	}
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		pending: map[int]chan reply{},
		events:  make(chan json.RawMessage, 256),
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Events returns the channel of unsolicited events (the `hello` handshake and
// `watch` state pushes). It is closed when the connection ends.
func (c *Client) Events() <-chan json.RawMessage { return c.events }

// Close ends the connection.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}

type envelope struct {
	ID     *int            `json:"id"`
	Event  string          `json:"event"`
	OK     *bool           `json:"ok"`
	Error  *Error          `json:"error"`
	Result json.RawMessage `json:"result"`
}

func (c *Client) readLoop() {
	defer close(c.closed)
	defer close(c.events)
	dec := json.NewDecoder(c.conn)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			c.fail(err)
			return
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue // ignore malformed lines
		}
		switch {
		case env.ID != nil:
			c.mu.Lock()
			ch := c.pending[*env.ID]
			delete(c.pending, *env.ID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if env.OK != nil && *env.OK {
				ch <- reply{result: env.Result}
			} else if env.Error != nil {
				ch <- reply{err: env.Error}
			} else {
				ch <- reply{err: &Error{Code: "internal", Message: "missing ok/error in response"}}
			}
		case env.Event != "":
			select {
			case c.events <- raw:
			default: // drop if nobody is keeping up
			}
		case env.Error != nil:
			// An error the client cannot match to a request: the mod answers a
			// request line it could not parse with {"id":null,...}. Without this
			// the reply is dropped and the caller's request just times out with
			// no reason (review B30).
			c.mu.Lock()
			c.strayErr = env.Error
			c.mu.Unlock()
		}
	}
}

// fail delivers the connection error to every pending call.
func (c *Client) fail(err error) {
	c.mu.Lock()
	for id, ch := range c.pending {
		ch <- reply{err: fmt.Errorf("connection closed: %w", err)}
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// Call sends a command and waits for its response.
func (c *Client) Call(cmd string, args any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan reply, 1)
	c.pending[id] = ch
	req := map[string]any{"id": id, "cmd": cmd}
	if args != nil {
		req["args"] = args
	}
	err := c.enc.Encode(req)
	if err != nil {
		// The request never went out, so nothing will ever answer it: drop the
		// slot instead of leaking it (review B32).
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case r := <-ch:
		return r.result, r.err
	case <-c.closed:
		return nil, fmt.Errorf("testbench: connection to the game closed")
	case <-time.After(60 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		stray := c.strayErr
		c.mu.Unlock()
		if stray != nil {
			return nil, fmt.Errorf("testbench: %s timed out; the mod reported: %w", cmd, stray)
		}
		return nil, fmt.Errorf("testbench: %s timed out", cmd)
	}
}

// StrayError returns the last error the mod sent that the client could not match
// to a request (typically a reply to a request line it could not parse), or nil.
func (c *Client) StrayError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.strayErr == nil {
		return nil // not `return c.strayErr`: that would be a non-nil interface
	}
	return c.strayErr
}

// CallInto sends a command and decodes the result into out.
func (c *Client) CallInto(cmd string, args, out any) error {
	raw, err := c.Call(cmd, args)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Ping checks the connection and returns the mod's handshake info.
func (c *Client) Ping() (*Hello, error) {
	var h Hello
	if err := c.CallInto("ping", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// ServerInfo is the game's current network session as reported by the `server`
// command. For a client, Address/Port identify the server it is connected to;
// when there is no meta session (Steam P2P) HostID/HostSteamID/Connections do.
type ServerInfo struct {
	State            string       `json:"state,omitempty"`
	Role             string       `json:"role,omitempty"` // "client" or "server"
	LocalIP          string       `json:"localIp,omitempty"`
	Name             string       `json:"name,omitempty"`
	Address          string       `json:"address,omitempty"`
	Port             string       `json:"port,omitempty"`
	Map              string       `json:"map,omitempty"`
	Version          string       `json:"version,omitempty"`
	Players          int          `json:"players,omitempty"`
	MaxPlayers       int          `json:"maxPlayers,omitempty"`
	Latency          int          `json:"latency,omitempty"`
	Password         bool         `json:"password,omitempty"`
	SteamID          string       `json:"steamId,omitempty"`
	HostID           string       `json:"hostId,omitempty"`
	HostSteamID      string       `json:"hostSteamId,omitempty"`
	HasP2P           string       `json:"hasP2P,omitempty"`
	Lobby            string       `json:"lobby,omitempty"`
	Transport        string       `json:"transport,omitempty"`
	Connections      []ConnSample `json:"connections,omitempty"`
	Error            string       `json:"error,omitempty"`
	ConnectionsError string       `json:"connectionsError,omitempty"`
}

// ConnSample is one live network connection (NetClientSample).
type ConnSample struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Ping int    `json:"ping"`
}

// Server returns the game's current network session.
func (c *Client) Server() (*ServerInfo, error) {
	var s ServerInfo
	if err := c.CallInto("server", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GameSessionRow is one server-browser entry (`serverlist`).
type GameSessionRow struct {
	SessionID  int    `json:"sessionId"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	Port       string `json:"port"`
	Map        string `json:"map"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"maxPlayers"`
	Latency    int    `json:"latency"`
	Password   bool   `json:"password"`
	Version    string `json:"version"`
	UpTime     int    `json:"uptime"`
	SteamID    string `json:"steamId"`
}

// ServerList returns the server-browser list the game last fetched.
func (c *Client) ServerList() ([]GameSessionRow, error) {
	var res struct {
		Servers []GameSessionRow `json:"servers"`
	}
	if err := c.CallInto("serverlist", nil, &res); err != nil {
		return nil, err
	}
	return res.Servers, nil
}

// Hello describes the running mod.
type Hello struct {
	Mod         string  `json:"mod"`
	Version     string  `json:"version"`
	GameVersion string  `json:"gameVersion"`
	Paused      bool    `json:"paused"`
	Chips       int     `json:"chips"`
	Line        float64 `json:"line"`
}

// Chip is one programmable chip as reported by chip.list.
type Chip struct {
	ID           int64    `json:"id"`
	Index        int      `json:"index"`
	Name         string   `json:"name"`
	Prefab       string   `json:"prefab"`
	Line         float64  `json:"line"`
	Lines        int      `json:"lines"`
	Programmable *bool    `json:"programmable,omitempty"`
	Powered      *bool    `json:"powered,omitempty"`
	Power        *float64 `json:"power,omitempty"`
	Pos          *ChipPos `json:"pos,omitempty"`
	Fingerprint  string   `json:"fp,omitempty"`
	Selected     bool     `json:"selected"`
}

// ChipPos is a host's world position (and yaw in degrees), reported by chip.list
// when the mod can read the host's transform.
type ChipPos struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Z   float64 `json:"z"`
	Yaw float64 `json:"yaw,omitempty"`
}

// String renders a position compactly, e.g. "12.3, 45.6, -7.8 (yaw 90)".
func (p *ChipPos) String() string {
	if p == nil {
		return "?"
	}
	s := fmt.Sprintf("%.1f, %.1f, %.1f", p.X, p.Y, p.Z)
	if p.Yaw != 0 {
		s += fmt.Sprintf(", yaw %.0f", p.Yaw)
	}
	return s
}

// Fingerprint returns the short hash the mod reports for a program's source: a
// SHA-1 of the source with CRLF/CR normalized to LF and trailing whitespace
// trimmed. Keep this in sync with GameApi.Fingerprint in the mod.
func Fingerprint(src string) string {
	norm := strings.ReplaceAll(src, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	norm = strings.TrimRightFunc(norm, unicode.IsSpace)
	sum := sha1.Sum([]byte(norm))
	return hex.EncodeToString(sum[:4])
}

// ListChips returns the chips in the running world.
func (c *Client) ListChips() ([]Chip, error) {
	var res struct {
		Chips []Chip `json:"chips"`
	}
	if err := c.CallInto("chip.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Chips, nil
}

// Player is a player (Human) as reported by the `players` command, sorted by
// distance from the local player. Body is true when the row is a body bag (the
// player is dead); Trackable says whether an entity was found in the world that
// the HUD can point at.
type Player struct {
	Name      string   `json:"name"`
	Self      bool     `json:"self"`
	Online    *bool    `json:"online,omitempty"`
	Body      bool     `json:"body,omitempty"`
	Trackable *bool    `json:"trackable,omitempty"`
	Dist      *float64 `json:"dist,omitempty"`
	Pos       *ChipPos `json:"pos,omitempty"`
}

// Players lists the players in the world.
func (c *Client) Players() ([]Player, error) {
	var res struct {
		Players []Player `json:"players"`
	}
	if err := c.CallInto("players", nil, &res); err != nil {
		return nil, err
	}
	return res.Players, nil
}

// HudPos is a position (and, for the player, view angles) in the HUD state.
type HudPos struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Yaw   float64 `json:"yaw,omitempty"`
	Pitch float64 `json:"pitch,omitempty"`
	Label string  `json:"label,omitempty"`
}

// HudState is the in-game overlay state.
type HudState struct {
	On     bool    `json:"on"`
	Offset float64 `json:"offset,omitempty"`
	Player *HudPos `json:"player,omitempty"`
	Target *HudPos `json:"target,omitempty"`
}

// Hud reads or updates the overlay: args may carry "on", "clear", or a
// "target" of {x,y,z} or {chip:<selector>}. A nil args just reads the state.
func (c *Client) Hud(args map[string]any) (*HudState, error) {
	if args == nil {
		args = map[string]any{}
	}
	var st HudState
	if err := c.CallInto("hud", args, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Push uploads compiled IC10 (optionally running loader chunks first).
func (c *Client) Push(code string, loaders []string, chip any) (*PushResult, error) {
	args := map[string]any{"code": code}
	if len(loaders) > 0 {
		args["loaders"] = loaders
	}
	if chip != nil {
		args["chip"] = chip
	}
	var res PushResult
	if err := c.CallInto("push", args, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// PushResult is the result of a push.
type PushResult struct {
	Chip    Chip `json:"chip"`
	Lines   int  `json:"lines"`
	Loaders int  `json:"loaderOps"`
	// CompileError is the chip's error state immediately after the source was
	// uploaded. The mod answers `ok:true` even when the program did not
	// compile -- the chip simply keeps running its previous program -- so a
	// caller that ignores this field silently asserts against the wrong
	// binary (review B17).
	CompileError *ChipErrors `json:"compileError"`
}

// DeviceWrite sets one device logic value.
type DeviceWrite struct {
	Port  string  `json:"port"`
	ID    *int    `json:"id,omitempty"`
	Logic string  `json:"logic"`
	Slot  *int    `json:"slot,omitempty"`
	Value float64 `json:"value"`
	// Conn, when set, addresses a cable-network channel: the target device is
	// resolved as usual (port/id) and then its connection Conn's network is
	// written (`dN:<conn> ChannelM`). Used to write Channel0..7 of a network.
	Conn *int `json:"conn,omitempty"`
}

// SetWrites applies device writes and returns how many were applied. When
// force is true it also writes logic the device reports as read-only (useful to
// simulate an input such as a dial).
func (c *Client) SetWrites(writes []DeviceWrite, chip any, force bool) (int, error) {
	args := map[string]any{"writes": writes}
	if force {
		args["force"] = true
	}
	if chip != nil {
		args["chip"] = chip
	}
	var res struct {
		Applied int `json:"applied"`
	}
	if err := c.CallInto("set", args, &res); err != nil {
		return 0, err
	}
	return res.Applied, nil
}

// Run advances the chip by ticks game ticks.
// Reset restarts the chip's program from line 1 without clearing registers
// (the mod's `reset`; see testdata/bench/ab/README.md for the phase caveat).
// Scenario runs use it between cases so each set/run/expect starts from a
// known PC; a chip that merely keeps running would drift across cases.
func (c *Client) Reset(chip any) error {
	args := map[string]any{}
	if chip != nil {
		args["chip"] = chip
	}
	return c.CallInto("reset", args, nil)
}

// Run executes n ticks (the mod's `run`).
func (c *Client) Run(ticks int, chip any) (*RunResult, error) {
	args := map[string]any{"ticks": ticks}
	if chip != nil {
		args["chip"] = chip
	}
	var res RunResult
	if err := c.CallInto("run", args, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// RunResult is the result of run.
type RunResult struct {
	Ticks int     `json:"ticks"`
	Line  float64 `json:"line"`
	// Error is the chip's error state after the run. A chip stuck on an error
	// line never advances (and re-running the faulting instruction has side
	// effects), so "the step succeeded" is meaningless then -- the mod still
	// answers ok:true, which makes ignoring this field a silent green (review
	// B22).
	Error *ChipErrors `json:"error"`
}

// Trace executes n instructions one at a time (the mod's `trace`) and returns
// the storage-instruction hits with register snapshots, so a debugger can step
// instruction by instruction. The world should be paused for determinism.
func (c *Client) Trace(n int, chip any) (*TraceResult, error) {
	args := map[string]any{"n": n}
	if chip != nil {
		args["chip"] = chip
	}
	var res TraceResult
	if err := c.CallInto("trace", args, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TraceResult is the result of trace.
type TraceResult struct {
	Steps    int        `json:"steps"`
	HitCount int        `json:"hitCount"`
	Hits     []TraceHit `json:"hits"`
}

// TraceHit is one executed storage instruction (set/get/put/poke/...) with the
// registers right before it ran.
type TraceHit struct {
	PC   int    `json:"pc"`
	Text string `json:"text"`
	Regs []Num  `json:"regs"`
}

// RunTo steps the chip one instruction at a time until it reaches a line (the
// mod's `runTo`), which is how the editor offers "run to cursor" in one round
// trip. max caps the instruction count, so a program that never reaches the line
// still returns.
func (c *Client) RunTo(line, max int, chip any) (*RunToResult, error) {
	args := map[string]any{"line": line, "max": max}
	if chip != nil {
		args["chip"] = chip
	}
	var res RunToResult
	if err := c.CallInto("runTo", args, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// RunToResult is the result of runTo. Reached reports whether the chip actually
// stopped on the requested line; when it is false the chip either errored or
// looped without getting there. It is a pointer because a mod older than 0.5.9
// does not send it (unknown, not "did not reach").
type RunToResult struct {
	Steps   int     `json:"steps"`
	Line    float64 `json:"line"`
	Reached *bool   `json:"reached"`
	// Error is set when the chip is stuck on an error line (see RunResult).
	Error *ChipErrors `json:"error"`
}

// PauseForRun makes sure the world is paused before a tool drives a chip, and
// returns a function that restores the previous state. Stepping a chip only
// does anything while paused (a multiplayer client cannot drive the host's
// simulation), and a scenario run against a live world is non-deterministic, so
// a pause that silently does not take effect is reported as an error rather
// than left as a green run (review B23). Only the host of a single-player world
// or a client-less server can pause.
func (c *Client) PauseForRun() (func() error, error) {
	noop := func() error { return nil }
	h, err := c.Ping()
	if err != nil {
		// Best effort: if even the hello fails the caller's next call reports
		// the broken connection.
		return noop, nil
	}
	if h.Paused {
		return noop, nil
	}
	var res struct {
		Paused    bool `json:"paused"`
		Requested bool `json:"requested"`
	}
	if err := c.CallInto("pause", map[string]any{"on": true}, &res); err != nil {
		return noop, fmt.Errorf("pause: %w", err)
	}
	if !res.Paused {
		return noop, errors.New("the game did not pause (only the host of a single-player or client-less server can); a run now would be non-deterministic")
	}
	return func() error {
		_, err := c.Call("pause", map[string]any{"on": false})
		return err
	}, nil
}

// SetReg pokes a chip register or stack slot while the world is paused, so a
// watch panel can change a value in place. Pass a register name ("r0".."r15",
// "sp", "ra") or a stack slot (stack >= 0).
func (c *Client) SetReg(reg string, stack int, value float64, chip any) (*SetRegResult, error) {
	args := map[string]any{"value": value}
	if stack >= 0 {
		args["stack"] = stack
	} else {
		args["reg"] = reg
	}
	if chip != nil {
		args["chip"] = chip
	}
	var res SetRegResult
	if err := c.CallInto("setreg", args, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SetRegResult is the result of setreg.
type SetRegResult struct {
	Reg   string  `json:"reg"`
	Stack *int    `json:"stack"`
	Value float64 `json:"value"`
}

// State fetches a chip state snapshot. include lists the sections to return
// (nil = all); all requests the whole stack.
func (c *Client) State(include []string, all bool, chip any) (*State, error) {
	args := map[string]any{}
	if len(include) > 0 {
		args["include"] = include
	}
	if all {
		args["all"] = true
	}
	if chip != nil {
		args["chip"] = chip
	}
	var st State
	if err := c.CallInto("state", args, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// State is a chip snapshot.
type State struct {
	Chip Chip `json:"chip"`
	// Registers maps a register name to its value; a nil entry is the protocol's
	// null (never written / quiet NaN), NOT 0 (review B21).
	Registers map[string]*Num `json:"registers"`
	Stack     *Stack          `json:"stack"`
	PC        int             `json:"pc"`
	Line      float64         `json:"line"`
	Program   *Program        `json:"program"`
	Errors    *ChipErrors     `json:"errors"`
	Devices   []DeviceState   `json:"devices"`
}

// Stack is the chip's persistent stack.
type Stack struct {
	Size   int             `json:"size"`
	SP     int             `json:"sp"`
	Values map[string]*Num `json:"values"`
}

// Program is program metadata.
type Program struct {
	Lines int `json:"lines"`
}

// Num is a JSON number that tolerates the game serializer's spellings and,
// used through a pointer, keeps null distinct from 0.
//
// Two protocol facts drive it (docs/ingame-testbench.md §4.3, review B21/P1):
// a `null` means "never written" (quiet NaN) and is explicitly NOT 0, and the
// game's serializer writes the non-finite values as the strings "NaN",
// "Infinity" and "-Infinity" -- valid JSON but the wrong type, which would fail
// a plain float64 field and destroy the whole response.
type Num float64

// UnmarshalJSON accepts a number, or one of the game's non-finite spellings.
// A JSON null never reaches here: the pointer stays nil.
func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	switch s {
	case "":
		return nil
	case "NaN":
		*n = Num(math.NaN())
	case "Infinity", "inf", "+Inf":
		*n = Num(math.Inf(1))
	case "-Infinity", "-inf":
		*n = Num(math.Inf(-1))
	default:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*n = Num(f)
	}
	return nil
}

// MarshalJSON keeps the JSON valid: a non-finite value is written as null
// (which is what the protocol means by it), never as the unsupported bare
// NaN/Infinity tokens.
func (n Num) MarshalJSON() ([]byte, error) {
	v := float64(n)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// String renders the value for terminals, spelling the non-finite ones the way
// the game does.
func (n Num) String() string {
	v := float64(n)
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ChipErrors is the chip's compile/runtime error state.
type ChipErrors struct {
	Code        string `json:"code"`
	Compilation bool   `json:"compilation"`
	Line        int    `json:"line"`
}

// Error implements error, so a chip error state can be reported directly. The
// game's message carries rich-text markup, which is stripped for plain-text
// surfaces like the CLI.
func (e *ChipErrors) Error() string {
	if e == nil {
		return ""
	}
	kind := "runtime"
	if e.Compilation {
		kind = "compile"
	}
	if code := stripMarkup(e.Code); code != "" {
		return kind + " error: " + code
	}
	return fmt.Sprintf("%s error at line %d", kind, e.Line)
}

// markupTag matches the game's rich-text tags (<color=red>, </color>, ...).
// Only tag-shaped runs are removed, so a message containing `<` as an operator
// survives.
var markupTag = regexp.MustCompile(`</?[a-zA-Z][^<>]*>`)

// stripMarkup removes rich-text tags and collapses whitespace.
func stripMarkup(s string) string {
	return strings.Join(strings.Fields(markupTag.ReplaceAllString(s, "")), " ")
}

// DeviceState is one device on a chip port.
type DeviceState struct {
	Port string `json:"port"`
	// ID is the device's ReferenceId (mod 0.5.11+). It is what `hud --id`
	// tracks, and what the editor addresses a device by when it is not wired to a
	// port. Nil for an older mod.
	ID      *int              `json:"id,omitempty"`
	Prefab  string            `json:"prefab"`
	Name    string            `json:"name"`
	Binding string            `json:"binding,omitempty"`
	Present *bool             `json:"present,omitempty"`
	Logic   map[string]*Num   `json:"logic"`
	Slots   []SlotState       `json:"slots,omitempty"`
	Probe   map[string]string `json:"probe,omitempty"`
	// Channels, when present, are the device's cable-network channels: one
	// entry per data connection, each with the eight shared Channel0..7 values
	// (IC10 `dN:<conn> ChannelM`). Nil for a device that is not on a network.
	Channels []ConnectionState `json:"channels,omitempty"`
}

// ConnectionState is one cable-network connection of a device: its connection
// index, the network's ReferenceId, and the eight Channel0..7 values (nil for a
// channel that has never been written).
type ConnectionState struct {
	Port     int        `json:"port"`
	Net      int        `json:"net,omitempty"`
	Channels []*float64 `json:"channels"`
}

// SlotState is one logic slot (inventory slot) of a device.
type SlotState struct {
	Index int             `json:"index"`
	Logic map[string]*Num `json:"logic"`
}
