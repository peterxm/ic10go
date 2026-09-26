// Package testbench is the Go client for the ic10go in-game testbench mod.
//
// The mod listens on 127.0.0.1:7800 and speaks newline-delimited JSON (one JSON
// object per line). This package handles the request/response correlation and
// exposes the unsolicited `state` events used for live views.
//
// See docs/ingame-testbench.md for the protocol and command set.
package testbench

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
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
		c.mu.Unlock()
		return nil, fmt.Errorf("testbench: %s timed out", cmd)
	}
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
	ID       int64   `json:"id"`
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Prefab   string  `json:"prefab"`
	Line     float64 `json:"line"`
	Lines    int     `json:"lines"`
	Selected bool    `json:"selected"`
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
	Chip    Chip            `json:"chip"`
	Lines   int             `json:"lines"`
	Loaders int             `json:"loaderOps"`
	Error   json.RawMessage `json:"compileError"`
}

// DeviceWrite sets one device logic value.
type DeviceWrite struct {
	Port  string  `json:"port"`
	Logic string  `json:"logic"`
	Slot  *int    `json:"slot,omitempty"`
	Value float64 `json:"value"`
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
	Chip      Chip               `json:"chip"`
	Registers map[string]float64 `json:"registers"`
	Stack     *Stack             `json:"stack"`
	PC        int                `json:"pc"`
	Line      float64            `json:"line"`
	Program   *Program           `json:"program"`
	Errors    *ChipErrors        `json:"errors"`
	Devices   []DeviceState      `json:"devices"`
}

// Stack is the chip's persistent stack.
type Stack struct {
	Size   int                `json:"size"`
	SP     int                `json:"sp"`
	Values map[string]float64 `json:"values"`
}

// Program is program metadata.
type Program struct {
	Lines int `json:"lines"`
}

// ChipErrors is the chip's compile/runtime error state.
type ChipErrors struct {
	Code        string `json:"code"`
	Compilation bool   `json:"compilation"`
	Line        int    `json:"line"`
}

// DeviceState is one device on a chip port.
type DeviceState struct {
	Port   string             `json:"port"`
	Prefab string             `json:"prefab"`
	Name   string             `json:"name"`
	Logic  map[string]float64 `json:"logic"`
}
