package source

import "fmt"

// Pos is a position in a source file. Line and Col are 1-based.
type Pos struct {
	File   string
	Line   int
	Col    int
	Offset int
}

// Comment is a source comment, used by the formatter to preserve them.
type Comment struct {
	Line     int
	Text     string
	Trailing bool // true when code precedes the comment on the same line
}

func (p Pos) IsValid() bool { return p.Line > 0 }

func (p Pos) String() string {
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// File is an in-memory source file with line index support.
type File struct {
	Name string
	Src  []byte

	lineOffsets []int
}

func NewFile(name string, src []byte) *File {
	f := &File{Name: name, Src: src}
	f.index()
	return f
}

func (f *File) index() {
	f.lineOffsets = append(f.lineOffsets, 0)
	for i, b := range f.Src {
		if b == '\n' {
			f.lineOffsets = append(f.lineOffsets, i+1)
		}
	}
}

// PosAt returns the position for a byte offset.
func (f *File) PosAt(offset int) Pos {
	if offset < 0 {
		offset = 0
	}
	if offset > len(f.Src) {
		offset = len(f.Src)
	}
	line := 1
	lo, hi := 0, len(f.lineOffsets)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if f.lineOffsets[mid] <= offset {
			line = mid + 1
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	col := offset - f.lineOffsets[line-1] + 1
	return Pos{File: f.Name, Line: line, Col: col, Offset: offset}
}

// LineText returns the text of the 1-based line, without the trailing newline.
func (f *File) LineText(line int) string {
	if line < 1 || line > len(f.lineOffsets) {
		return ""
	}
	start := f.lineOffsets[line-1]
	end := len(f.Src)
	if line < len(f.lineOffsets) {
		end = f.lineOffsets[line] - 1
	}
	if end > 0 && end <= len(f.Src) && f.Src[end-1] == '\r' {
		end--
	}
	return string(f.Src[start:end])
}

// LineCount returns the number of lines in the file.
func (f *File) LineCount() int { return len(f.lineOffsets) }
