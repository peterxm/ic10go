package main

import "testing"

// TestParseWrite covers `set`/`get` operand parsing: port logic, the port's
// network connection (`dN:C` — a cable-network channel), and device-by-id,
// including the id form with a connection (`id:N:C`).
func TestParseWrite(t *testing.T) {
	cases := []struct {
		in    string
		port  string
		id    int // -1 when unset
		conn  int // -1 when unset
		logic string
		value float64
	}{
		{"d1.Setting=10", "d1", -1, -1, "Setting", 10},
		{"db.On=1", "db", -1, -1, "On", 1},
		{"d1:0.Channel0=1", "d1:0", -1, -1, "Channel0", 1},
		{"db:0.Channel3=0.5", "db:0", -1, -1, "Channel3", 0.5},
		{"id:12327.Channel0=1", "", 12327, -1, "Channel0", 1},
		{"id:12327:1.Channel7=2", "", 12327, 1, "Channel7", 2},
	}
	for _, c := range cases {
		w, ok := parseWrite(c.in)
		if !ok {
			t.Errorf("%q: parse failed", c.in)
			continue
		}
		if w.Port != c.port {
			t.Errorf("%q: port = %q, want %q", c.in, w.Port, c.port)
		}
		if c.id < 0 {
			if w.ID != nil {
				t.Errorf("%q: id = %d, want none", c.in, *w.ID)
			}
		} else if w.ID == nil || *w.ID != c.id {
			t.Errorf("%q: id = %v, want %d", c.in, w.ID, c.id)
		}
		if c.conn < 0 {
			if w.Conn != nil {
				t.Errorf("%q: conn = %d, want none", c.in, *w.Conn)
			}
		} else if w.Conn == nil || *w.Conn != c.conn {
			t.Errorf("%q: conn = %v, want %d", c.in, w.Conn, c.conn)
		}
		if w.Logic != c.logic {
			t.Errorf("%q: logic = %q, want %q", c.in, w.Logic, c.logic)
		}
		if w.Value != c.value {
			t.Errorf("%q: value = %v, want %v", c.in, w.Value, c.value)
		}
	}
	for _, bad := range []string{"", "d1", "d1.Setting", "setting=1", "id:x.Channel0=1"} {
		if _, ok := parseWrite(bad); ok {
			t.Errorf("%q: expected parse to fail", bad)
		}
	}
}
