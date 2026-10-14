package testbench

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
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
	// Env marks a case that could not run because of the environment (no chip,
	// an unknown logic name, the connection dropping) rather than because the
	// program under test behaved wrongly. The two must not share an exit code:
	// on CI a broken rig has to be distinguishable from a codegen regression
	// (review B28).
	Env bool `json:"envError,omitempty"`
}

// Report is the outcome of a scenario run.
type Report struct {
	Cases  []CaseResult `json:"cases"`
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
	// EnvFailed counts cases that failed for an environmental reason.
	EnvFailed int `json:"envFailed,omitempty"`
}

// OK reports whether every case passed.
func (r *Report) OK() bool { return r.Failed == 0 && r.EnvFailed == 0 && len(r.Cases) > 0 }

func (r *Report) add(c CaseResult) {
	switch {
	case c.Passed:
		r.Passed++
	case c.Env:
		r.EnvFailed++
	default:
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
			mark := "✗"
			if c.Env {
				mark = "!" // could not run: the rig, not the program
			}
			fmt.Fprintf(&b, "%s %s: %s\n", mark, name, c.Err)
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
	if r.EnvFailed > 0 {
		fmt.Fprintf(&b, ", %d could not run (environment)", r.EnvFailed)
	}
	return b.String()
}

// Runner executes a compiled program against a connected testbench.
type Runner struct {
	C       *Client
	Code    string
	Loaders []string
	// Reset restarts the chip before the first case and between cases so each
	// case's set/run/expect starts at the program top. Cases whose program is
	// phase-sensitive (tick-counted state machines) need it; without it the
	// PC drifts from case to case (the ab_trace README's reset caveat).
	Reset bool
	// KeepFailed optionally retains the chip paused after a failure; unused in v1.
	KeepFailed bool
	// AllowRigDrift skips the wiring check (see CheckRig) for a rig that
	// legitimately differs from the scenario's declared ports.
	AllowRigDrift bool
}

// Run uploads the program and runs every case.
func (r *Runner) Run(s *Scenario) (*Report, error) {
	var chip any
	if len(s.Chip) > 0 {
		if err := json.Unmarshal(s.Chip, &chip); err != nil {
			return nil, fmt.Errorf("bad chip selector: %w", err)
		}
	}
	res, err := r.C.Push(r.Code, r.Loaders, chip)
	if err != nil {
		return nil, err
	}
	if res.CompileError != nil {
		// The mod answers ok:true and leaves the chip running whatever program
		// it had, so continuing here would assert against the wrong binary and
		// report a green run (review B17).
		return nil, fmt.Errorf("push did not compile; the chip still runs its previous program: %w", res.CompileError)
	}

	if !r.AllowRigDrift {
		st, err := r.C.State([]string{"devices"}, false, chip)
		if err != nil {
			return nil, fmt.Errorf("rig check: %w", err)
		}
		if err := CheckRig(s, st); err != nil {
			return nil, err
		}
	}

	rep := &Report{}
	if r.Reset {
		// Reset right after the push too: the chip may have been left
		// mid-program by whatever ran before.
		if err := r.C.Reset(chip); err != nil {
			return nil, fmt.Errorf("reset: %w", err)
		}
	}
	for _, cs := range s.Cases {
		if r.Reset && len(rep.Cases) > 0 {
			if err := r.C.Reset(chip); err != nil {
				rep.add(CaseResult{Name: cs.Name, Err: "reset: " + err.Error(), Env: true})
				continue
			}
		}
		rep.add(r.runCase(cs, chip, s.ForceSet))
	}
	return rep, nil
}

func (r *Runner) runCase(cs Case, chip any, force bool) CaseResult {
	name := cs.Name
	// env marks an error that came out of a client call: the rig, not the
	// program, is at fault (review B28).
	env := func(err error) CaseResult {
		return CaseResult{Name: name, Err: err.Error(), Env: true}
	}

	writes := make([]DeviceWrite, 0, len(cs.Set))
	for k, v := range cs.Set {
		port, logic, err := splitKey(k)
		if err != nil {
			return env(err)
		}
		writes = append(writes, DeviceWrite{Port: port, Logic: logic, Value: v})
	}
	if len(writes) > 0 {
		if _, err := r.C.SetWrites(writes, chip, force); err != nil {
			return env(err)
		}
	}

	if cs.Run > 0 {
		rr, err := r.C.Run(cs.Run, chip)
		if err != nil {
			return env(err)
		}
		if rr.Error != nil {
			// A chip stuck on an error line never advances, so the run did not
			// do what the case assumes and the assertions below would be
			// meaningless (review B22).
			return CaseResult{Name: name, Err: "the chip is stuck on an error, so this case proves nothing: " + rr.Error.Error()}
		}
	}

	res := CaseResult{Name: name, Passed: true}

	if len(cs.Expect) > 0 {
		reads := make([]DeviceRead, 0, len(cs.Expect))
		for k := range cs.Expect {
			port, logic, err := splitKey(k)
			if err != nil {
				// A malformed key in the scenario file is a rig/config problem,
				// not a codegen regression (review B28).
				return CaseResult{Name: name, Err: err.Error(), Env: true}
			}
			reads = append(reads, DeviceRead{Port: port, Logic: logic})
		}
		got, err := r.C.GetValues(reads, chip)
		if err != nil {
			return env(err)
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
			return env(err)
		}
		for k, want := range cs.ExpectReg {
			g, ok := st.Registers[k]
			if !ok || g == nil {
				// A null register is "never written" (quiet NaN), which is
				// explicitly not 0 -- it must not satisfy an expectation of 0
				// (review B21).
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: "?"})
			} else if !closeEnough(want, float64(*g)) {
				res.Failures = append(res.Failures, Failure{Key: k, Want: fmtNum(want), Got: fmtNum(float64(*g))})
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

// CheckRig compares a scenario's declared ports with the chip's actual wiring
// and reports every mismatch at once. Without it a save whose devices drifted
// turns into a pile of confusing case failures that look like compiler
// regressions -- the 2026-10-14 review spent a whole round on exactly that (its
// §1.2: A CHIP d3 held a LogicMemory where every printer scenario needs an
// ElectronicsPrinter). A role matches when the device's prefab name contains it,
// so `ElectronicsPrinter` needs `StructureElectronicsPrinter`; a role that names
// no device kind is skipped.
func CheckRig(s *Scenario, st *State) error {
	if s == nil || len(s.Ports) == 0 || st == nil {
		return nil
	}
	got := map[string]string{}
	for _, d := range st.Devices {
		got[d.Port] = d.Prefab
	}
	var problems []string
	for port, role := range s.Ports {
		if isNotADeviceRole(role) {
			continue
		}
		prefab, ok := got[port]
		if !ok || prefab == "" {
			problems = append(problems, fmt.Sprintf("%s: nothing is wired there (the scenario wants %s)", port, role))
			continue
		}
		if !strings.Contains(strings.ToLower(prefab), strings.ToLower(role)) {
			problems = append(problems, fmt.Sprintf("%s: wired to %s (the scenario wants %s)", port, prefab, role))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("the rig does not match the scenario (pass --allow-rig-drift to run anyway):\n  %s",
		strings.Join(problems, "\n  "))
}

// isNotADeviceRole reports roles that name no particular device kind.
func isNotADeviceRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "spare", "any", "any device", "none":
		return true
	}
	return false
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
