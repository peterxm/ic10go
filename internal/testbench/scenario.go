package testbench

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Scenario is a test-bench script: compile `program`, upload it, then run the
// cases. See docs/ingame-testbench.md §6.1.
type Scenario struct {
	Program  string            `json:"program"`
	Args     []string          `json:"args,omitempty"`
	Chip     json.RawMessage   `json:"chip,omitempty"`  // chip selector: {"name":...} etc.
	Ports    map[string]string `json:"ports,omitempty"` // port -> role, for humans and VM diff
	Reset    *bool             `json:"reset,omitempty"`
	ForceSet bool              `json:"forceSet,omitempty"` // force device writes past CanLogicWrite
	Cases    []Case            `json:"cases"`
}

// Case is one set/run/expect triple.
type Case struct {
	Name      string             `json:"name,omitempty"`
	Set       map[string]float64 `json:"set,omitempty"`
	Run       int                `json:"run,omitempty"`
	Expect    map[string]float64 `json:"expect,omitempty"`
	ExpectReg map[string]float64 `json:"expectReg,omitempty"`
}

// LoadScenario reads a scenario JSON file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Program == "" {
		return nil, fmt.Errorf("%s: \"program\" is required", path)
	}
	return &s, nil
}

// Failure is one mismatch in a case.
type Failure struct {
	Key  string `json:"key"`
	Want string `json:"want"`
	Got  string `json:"got"`
}

// CaseResult is the outcome of one case.
type CaseResult struct {
	Name     string    `json:"name"`
	Passed   bool      `json:"passed"`
	Failures []Failure `json:"failures,omitempty"`
	Err      string    `json:"error,omitempty"`
}

// Report is the outcome of a scenario run.
type Report struct {
	Cases  []CaseResult `json:"cases"`
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
}

// OK reports whether every case passed.
func (r *Report) OK() bool { return r.Failed == 0 && len(r.Cases) > 0 }

func (r *Report) add(c CaseResult) {
	if c.Passed {
		r.Passed++
	} else {
		r.Failed++
	}
	r.Cases = append(r.Cases, c)
}

// String renders a compact human-readable report.
func (r *Report) String() string {
	var b strings.Builder
	for i, c := range r.Cases {
		name := c.Name
		if name == "" {
			name = "case " + strconv.Itoa(i+1)
		}
		if c.Err != "" {
			fmt.Fprintf(&b, "✗ %s: %s\n", name, c.Err)
			continue
		}
		if c.Passed {
			fmt.Fprintf(&b, "✓ %s\n", name)
			continue
		}
		fmt.Fprintf(&b, "✗ %s\n", name)
		for _, f := range c.Failures {
			fmt.Fprintf(&b, "    %s: want %s, got %s\n", f.Key, f.Want, f.Got)
		}
	}
	fmt.Fprintf(&b, "\n%d passed, %d failed", r.Passed, r.Failed)
	return b.String()
}

// Runner executes a compiled program against a connected testbench.
type Runner struct {
	C       *Client
	Code    string
	Loaders []string
	// KeepFailed optionally retains the chip paused after a failure; unused in v1.
}

// Run uploads the program and runs every case.
func (r *Runner) Run(s *Scenario) (*Report, error) {
	var chip any
	if len(s.Chip) > 0 {
		if err := json.Unmarshal(s.Chip, &chip); err != nil {
			return nil, fmt.Errorf("bad chip selector: %w", err)
		}
	}
	if _, err := r.C.Push(r.Code, r.Loaders, chip); err != nil {
		return nil, err
	}

	rep := &Report{}
	for _, cs := range s.Cases {
		rep.add(r.runCase(cs, chip, s.ForceSet))
	}
	return rep, nil
}

func (r *Runner) runCase(cs Case, chip any, force bool) CaseResult {
	name := cs.Name

	writes := make([]DeviceWrite, 0, len(cs.Set))
	for k, v := range cs.Set {
		port, logic, err := splitKey(k)
		if err != nil {
			return CaseResult{Name: name, Err: err.Error()}
		}
		writes = append(writes, DeviceWrite{Port: port, Logic: logic, Value: v})
	}
	if len(writes) > 0 {
		if _, err := r.C.SetWrites(writes, chip, force); err != nil {
			return CaseResult{Name: name, Err: err.Error()}
		}
	}

	if cs.Run > 0 {
		if _, err := r.C.Run(cs.Run, chip); err != nil {
			return CaseResult{Name: name, Err: err.Error()}
		}
	}

	res := CaseResult{Name: name, Passed: true}

	if len(cs.Expect) > 0 {
		reads := make([]DeviceRead, 0, len(cs.Expect))
		for k := range cs.Expect {
			port, logic, err := splitKey(k)
			if err != nil {
				return CaseResult{Name: name, Err: err.Error()}
			}
			reads = append(reads, DeviceRead{Port: port, Logic: logic})
		}
		got, err := r.C.GetValues(reads, chip)
		if err != nil {
			return CaseResult{Name: name, Err: err.Error()}
		}
		for k, want := range cs.Expect {
			g, ok := got[k]
			if !ok {
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: "?"})
			} else if !closeEnough(want, g) {
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: fmtNum(g)})
			}
		}
	}

	if len(cs.ExpectReg) > 0 {
		st, err := r.C.State([]string{"registers"}, false, chip)
		if err != nil {
			return CaseResult{Name: name, Err: err.Error()}
		}
		for k, want := range cs.ExpectReg {
			g, ok := st.Registers[k]
			if !ok {
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: "?"})
			} else if !closeEnough(want, g) {
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: fmtNum(g)})
			}
		}
	}

	res.Passed = len(res.Failures) == 0
	return res
}

// DeviceRead is one read request for get.
type DeviceRead struct {
	Port  string `json:"port"`
	Logic string `json:"logic"`
	Slot  *int   `json:"slot,omitempty"`
}

// GetValues reads several device values in one round-trip, keyed by
// "dN.Logic".
func (c *Client) GetValues(reads []DeviceRead, chip any) (map[string]float64, error) {
	args := map[string]any{"reads": reads}
	if chip != nil {
		args["chip"] = chip
	}
	var res struct {
		Values []struct {
			Port  string   `json:"port"`
			Logic string   `json:"logic"`
			Value *float64 `json:"value"`
		} `json:"values"`
	}
	if err := c.CallInto("get", args, &res); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(res.Values))
	for _, v := range res.Values {
		if v.Value == nil {
			continue
		}
		out[v.Port+"."+v.Logic] = *v.Value
	}
	return out, nil
}

// splitKey parses "d1.Setting" into a port and a logic name.
func splitKey(key string) (port, logic string, err error) {
	dot := strings.IndexByte(key, '.')
	if dot <= 0 || dot == len(key)-1 {
		return "", "", fmt.Errorf("bad device key %q (want dN.Logic, e.g. d1.Setting)", key)
	}
	port = key[:dot]
	if port[0] != 'd' && port[0] != 'D' {
		port = "d" + port
	}
	return port, key[dot+1:], nil
}

func closeEnough(want, got float64) bool {
	d := math.Abs(want - got)
	return d <= 1e-6*math.Max(1, math.Abs(want))
}

func fmtNum(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
