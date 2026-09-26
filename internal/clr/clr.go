// Package clr is a minimal ECMA-335 metadata reader, enough to enumerate a
// .NET assembly's types, fields, methods (with parameter names) and enum
// members. It needs no .NET SDK and no running game, only the DLL.
//
// It powers tools/genenums (game enum tables) and tools/dumpgameapi (game API
// verification), so both read the game assembly the same way.
package clr

import (
	"debug/pe"
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
)

// ---------------------------------------------------------------------------
// Minimal ECMA-335 metadata reader
// ---------------------------------------------------------------------------

const (
	tblModule          = 0x00
	tblTypeRef         = 0x01
	tblTypeDef         = 0x02
	tblFieldPtr        = 0x03
	tblField           = 0x04
	tblMethodPtr       = 0x05
	tblMethodDef       = 0x06
	tblParamPtr        = 0x07
	tblParam           = 0x08
	tblInterfaceImp    = 0x09
	tblMemberRef       = 0x0A
	tblConstant        = 0x0B
	tblCustomAttribute = 0x0C
	tblFieldMarshal    = 0x0D
	tblDeclSecurity    = 0x0E
	tblClassLayout     = 0x0F
	tblFieldLayout     = 0x10
	tblStandAloneSig   = 0x11
	tblEventMap        = 0x12
	tblEventPtr        = 0x13
	tblEvent           = 0x14
	tblPropertyMap     = 0x15
	tblPropertyPtr     = 0x16
	tblProperty        = 0x17
	tblMethodSemantics = 0x18
	tblMethodImpl      = 0x19
	tblModuleRef       = 0x1A
	tblTypeSpec        = 0x1B
	tblImplMap         = 0x1C
	tblFieldRVA        = 0x1D
	tblAssembly        = 0x20
	tblAssemblyRef     = 0x23
	tblFile            = 0x26
	tblExportedType    = 0x27
	tblManifestRes     = 0x28
	tblNestedClass     = 0x29
)

type File struct {
	data     []byte
	strOff   int
	blobOff  int
	rows     [64]uint32
	tableOff [64]int
	nested   map[int]int // nested TypeDef index -> enclosing TypeDef index
	strSize  int
	blobSize int
	guidSize int
}

func openTables(f *pe.File, path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	rvaToOff := func(rva uint32) uint32 {
		for _, s := range f.Sections {
			size := s.VirtualSize
			if size == 0 {
				size = s.Size
			}
			if rva >= s.VirtualAddress && rva < s.VirtualAddress+size {
				return rva - s.VirtualAddress + s.Offset
			}
		}
		return 0
	}

	var cliRVA uint32
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		cliRVA = oh.DataDirectory[14].VirtualAddress
	case *pe.OptionalHeader32:
		cliRVA = oh.DataDirectory[14].VirtualAddress
	default:
		return nil, fmt.Errorf("unsupported PE optional header")
	}
	if cliRVA == 0 {
		return nil, fmt.Errorf("no CLI header; not a .NET assembly?")
	}

	cliOff := int(rvaToOff(cliRVA))
	if cliOff <= 0 || cliOff+16 > len(data) {
		return nil, fmt.Errorf("CLI header outside the file")
	}
	metaRVA := binary.LittleEndian.Uint32(data[cliOff+8:])
	metaOff := int(rvaToOff(metaRVA))
	if metaOff <= 0 || metaOff+20 > len(data) {
		return nil, fmt.Errorf("metadata root outside the file")
	}
	if string(data[metaOff:metaOff+4]) != "BSJB" {
		return nil, fmt.Errorf("bad metadata signature")
	}

	verLen := int(binary.LittleEndian.Uint32(data[metaOff+12:]))
	p := metaOff + 16 + verLen
	if p+4 > len(data) {
		return nil, fmt.Errorf("truncated metadata header")
	}
	streams := int(binary.LittleEndian.Uint16(data[p+2:]))
	q := p + 4

	var tablesOff, strOff, blobOff int
	for i := 0; i < streams; i++ {
		if q+8 > len(data) {
			return nil, fmt.Errorf("truncated stream header")
		}
		offset := int(binary.LittleEndian.Uint32(data[q:]))
		q += 4
		// size is unused; the stream name follows
		q += 4
		name, nq := readCString(data, q)
		q = align4(nq)
		switch name {
		case "#~":
			tablesOff = metaOff + offset
		case "#Strings":
			strOff = metaOff + offset
		case "#Blob":
			blobOff = metaOff + offset
		}
	}
	if tablesOff == 0 || strOff == 0 {
		return nil, fmt.Errorf("metadata stream #~ or #Strings missing")
	}

	t := &File{data: data, strOff: strOff, blobOff: blobOff}
	if tablesOff+24 > len(data) {
		return nil, fmt.Errorf("table stream outside the file")
	}
	heapSizes := data[tablesOff+6]
	t.strSize = sizeOfFlag(heapSizes, 0x01)
	t.guidSize = sizeOfFlag(heapSizes, 0x02)
	t.blobSize = sizeOfFlag(heapSizes, 0x04)

	valid := binary.LittleEndian.Uint64(data[tablesOff+8:])
	numPresent := bits.OnesCount64(valid)
	cursor := tablesOff + 24 + 4*numPresent
	for id := 0; id < 64; id++ {
		if valid&(1<<uint(id)) == 0 {
			continue
		}
		t.rows[id] = binary.LittleEndian.Uint32(data[tablesOff+24+4*idxOfPresent(valid, id):])
		t.tableOff[id] = cursor
		rs, err := t.rowSize(id)
		if err != nil {
			return nil, err
		}
		cursor += int(t.rows[id]) * rs
		if cursor > len(data) {
			return nil, fmt.Errorf("table %d runs past the file", id)
		}
	}

	t.nested = map[int]int{}
	if t.rows[tblNestedClass] > 0 {
		idx := t.tableIndex(tblTypeDef)
		rs := 2 * idx
		for i := 1; i <= int(t.rows[tblNestedClass]); i++ {
			off := t.tableOff[tblNestedClass] + (i-1)*rs
			inner := readIndex(t.data, off, idx)
			outer := readIndex(t.data, off+idx, idx)
			t.nested[inner] = outer
		}
	}
	return t, nil
}

func idxOfPresent(valid uint64, id int) int {
	n := 0
	for i := 0; i < id; i++ {
		if valid&(1<<uint(i)) != 0 {
			n++
		}
	}
	return n
}

func sizeOfFlag(heapSizes, flag byte) int {
	if heapSizes&flag != 0 {
		return 4
	}
	return 2
}

// ---------------------------------------------------------------------------
// Table row sizes
// ---------------------------------------------------------------------------

func (t *File) codedIndex(tags []int, tagBits int) int {
	var max uint32
	for _, id := range tags {
		if t.rows[id] > max {
			max = t.rows[id]
		}
	}
	if max < (1 << uint(16-tagBits)) {
		return 2
	}
	return 4
}

func (t *File) tableIndex(id int) int {
	if t.rows[id] < 0x10000 {
		return 2
	}
	return 4
}

func (t *File) rowSize(id int) (int, error) {
	s, b, g := t.strSize, t.blobSize, t.guidSize
	tdr := t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2)
	switch id {
	case tblModule:
		return 2 + s + g + g + g, nil
	case tblTypeRef:
		return t.codedIndex([]int{tblModule, tblModuleRef, tblAssemblyRef, tblTypeRef}, 2) + s + s, nil
	case tblTypeDef:
		return 4 + s + s + tdr + t.tableIndex(tblField) + t.tableIndex(tblMethodDef), nil
	case tblFieldPtr:
		return t.tableIndex(tblField), nil
	case tblField:
		return 2 + s + b, nil
	case tblMethodPtr:
		return t.tableIndex(tblMethodDef), nil
	case tblMethodDef:
		return 4 + 2 + 2 + s + b + t.tableIndex(tblParam), nil
	case tblParamPtr:
		return t.tableIndex(tblParam), nil
	case tblParam:
		return 2 + 2 + s, nil
	case tblInterfaceImp:
		return t.tableIndex(tblTypeDef) + tdr, nil
	case tblMemberRef:
		return t.codedIndex([]int{tblTypeDef, tblTypeRef, tblModuleRef, tblMethodDef, tblTypeSpec}, 3) + s + b, nil
	case tblConstant:
		return 1 + 1 + t.codedIndex([]int{tblField, tblParam, tblProperty}, 2) + b, nil
	case tblCustomAttribute:
		hasCA := t.codedIndex([]int{tblMethodDef, tblField, tblTypeRef, tblTypeDef, tblParam, tblInterfaceImp, tblMemberRef, tblModule, tblDeclSecurity, tblProperty, tblEvent, tblStandAloneSig, tblModuleRef, tblTypeSpec, tblAssembly, tblAssemblyRef, tblFile, tblExportedType, tblManifestRes, 0x2A, 0x2C, 0x2B}, 5)
		caType := t.codedIndex([]int{tblMethodDef, tblMemberRef}, 3)
		return hasCA + caType + b, nil
	case tblFieldMarshal:
		return t.codedIndex([]int{tblField, tblParam}, 1) + b, nil
	case tblDeclSecurity:
		return 2 + t.codedIndex([]int{tblTypeDef, tblMethodDef, tblAssembly}, 2) + b, nil
	case tblClassLayout:
		return 2 + 4 + t.tableIndex(tblTypeDef), nil
	case tblFieldLayout:
		return 4 + t.tableIndex(tblField), nil
	case tblStandAloneSig:
		return b, nil
	case tblEventMap:
		return t.tableIndex(tblTypeDef) + t.tableIndex(tblEvent), nil
	case tblEventPtr:
		return t.tableIndex(tblEvent), nil
	case tblEvent:
		return 2 + s + tdr, nil
	case tblPropertyMap:
		return t.tableIndex(tblTypeDef) + t.tableIndex(tblProperty), nil
	case tblPropertyPtr:
		return t.tableIndex(tblProperty), nil
	case tblProperty:
		return 2 + s + b, nil
	case tblMethodSemantics:
		return 2 + t.tableIndex(tblMethodDef) + t.codedIndex([]int{tblEvent, tblProperty}, 1), nil
	case tblMethodImpl:
		return t.tableIndex(tblTypeDef) + t.codedIndex([]int{tblMethodDef, tblMemberRef}, 1) + t.codedIndex([]int{tblMethodDef, tblMemberRef}, 1), nil
	case tblModuleRef:
		return s, nil
	case tblTypeSpec:
		return b, nil
	case tblImplMap:
		return 2 + t.codedIndex([]int{tblField, tblMethodDef}, 1) + s + t.tableIndex(tblModuleRef), nil
	case tblFieldRVA:
		return 4 + t.tableIndex(tblField), nil
	case 0x1E: // ENCLog
		return 4 + 4, nil
	case 0x1F: // ENCMap
		return 4, nil
	case tblAssembly:
		return 4 + 2 + 2 + 2 + 2 + 4 + b + s + s, nil
	case 0x21: // AssemblyProcessor
		return 4, nil
	case 0x22: // AssemblyOS
		return 4 + 4 + 4, nil
	case tblAssemblyRef:
		return 2 + 2 + 2 + 2 + 4 + b + s + s + b, nil
	case 0x24: // AssemblyRefProcessor
		return 4 + t.tableIndex(tblAssemblyRef), nil
	case 0x25: // AssemblyRefOS
		return 4 + 4 + 4 + t.tableIndex(tblAssemblyRef), nil
	case tblFile:
		return 4 + s + b, nil
	case tblExportedType:
		return 4 + 4 + s + s + t.codedIndex([]int{tblFile, tblAssemblyRef, tblExportedType}, 2), nil
	case tblManifestRes:
		return 4 + 4 + s + t.codedIndex([]int{tblFile, tblAssemblyRef, tblExportedType}, 2), nil
	case tblNestedClass:
		return t.tableIndex(tblTypeDef) + t.tableIndex(tblTypeDef), nil
	default:
		return 0, nil
	}
}

// ---------------------------------------------------------------------------
// Reading rows
// ---------------------------------------------------------------------------

func (t *File) u16(off int) int { return int(binary.LittleEndian.Uint16(t.data[off:])) }
func (t *File) u32(off int) int { return int(binary.LittleEndian.Uint32(t.data[off:])) }

func (t *File) str(idx int) string {
	if idx == 0 {
		return ""
	}
	s, _ := readCString(t.data, t.strOff+idx)
	return s
}

// typeRef returns namespace + "+"-joined name for a 1-based TypeRef row.
func (t *File) typeRef(i int) (string, string, error) {
	if i < 1 || i > int(t.rows[tblTypeRef]) {
		return "", "", fmt.Errorf("TypeRef index %d out of range", i)
	}
	rs, _ := t.rowSize(tblTypeRef)
	off := t.tableOff[tblTypeRef] + (i-1)*rs
	off += t.codedIndex([]int{tblModule, tblModuleRef, 0x23, tblTypeRef}, 2)
	nameIdx := readIndex(t.data, off, t.strSize)
	off += t.strSize
	nsIdx := readIndex(t.data, off, t.strSize)
	return t.str(nsIdx), t.str(nameIdx), nil
}

// enumMembers returns member name -> value for the enum with the given full
// name ("Namespace.Type" or "Namespace.Outer+Inner").
func (t *File) EnumMembers(fullName string) (map[string]int64, error) {
	tdRS, _ := t.rowSize(tblTypeDef)
	fieldStart := t.tableOff[tblField]

	// Pass 1: find the TypeDef and its field range.
	tdIndex := -1
	first := 0
	last := 0
	for i := 1; i <= int(t.rows[tblTypeDef]); i++ {
		off := t.tableOff[tblTypeDef] + (i-1)*tdRS
		extends := readIndex(t.data, off+4+2*t.strSize, t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2))
		fieldList := readIndex(t.data, off+4+2*t.strSize+t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2), t.tableIndex(tblField))
		if !t.extendsEnum(extends) {
			continue
		}
		full, err := t.FullTypeName(i)
		if err != nil {
			return nil, err
		}
		if full != fullName {
			continue
		}
		tdIndex = i
		first = fieldList
		if i < int(t.rows[tblTypeDef]) {
			// Next TypeDef's FieldList is the exclusive end.
			noff := t.tableOff[tblTypeDef] + i*tdRS
			last = readIndex(t.data, noff+4+2*t.strSize+t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2), t.tableIndex(tblField))
		} else {
			last = int(t.rows[tblField]) + 1
		}
		break
	}
	if tdIndex < 0 {
		return nil, fmt.Errorf("type not found")
	}

	// Pass 2: values from the Constant table, keyed by Field index.
	constRS, _ := t.rowSize(tblConstant)
	values := map[int]int64{}
	if t.rows[tblConstant] > 0 {
		parentSize := t.codedIndex([]int{tblField, tblParam, tblProperty}, 2)
		for i := 1; i <= int(t.rows[tblConstant]); i++ {
			off := t.tableOff[tblConstant] + (i-1)*constRS
			ctype := int(t.data[off])
			parent := readIndex(t.data, off+2, parentSize)
			if parent&0x3 != 0 { // tag 0 = Field
				continue
			}
			fieldIdx := parent >> 2
			blobIdx := readIndex(t.data, off+2+parentSize, t.blobSize)
			v, ok := t.blobInt(blobIdx, ctype)
			if ok {
				values[fieldIdx] = v
			}
		}
	}

	// Pass 3: literal fields in the range.
	fieldRS, _ := t.rowSize(tblField)
	members := map[string]int64{}
	for i := first; i < last && i <= int(t.rows[tblField]); i++ {
		off := fieldStart + (i-1)*fieldRS
		flags := t.u16(off)
		if flags&0x0040 == 0 { // not Literal
			continue
		}
		nameIdx := readIndex(t.data, off+2, t.strSize)
		name := t.str(nameIdx)
		if name == "" || name == "value__" {
			continue
		}
		v, ok := values[i]
		if !ok {
			v = 0
		}
		if _, exists := members[name]; !exists {
			members[name] = v
		}
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("no literal members (is this an enum?)")
	}
	return members, nil
}

// extendsEnum reports whether a TypeDefOrRef coded index points at System.Enum.
func (t *File) extendsEnum(coded int) bool {
	tag := coded & 0x3
	idx := coded >> 2
	if tag != 1 { // 1 = TypeRef
		return false
	}
	ns, name, err := t.typeRef(idx)
	if err != nil {
		return false
	}
	return ns == "System" && name == "Enum"
}

// qualified joins a namespace and a (possibly nested) type name.
func qualified(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + "." + name
}

// fullTypeName returns the full name of the TypeDef at 1-based index i,
// resolving nested types to "Namespace.Outer+Inner".
func (t *File) FullTypeName(i int) (string, error) {
	if i < 1 || i > int(t.rows[tblTypeDef]) {
		return "", fmt.Errorf("TypeDef index %d out of range", i)
	}
	tdRS, _ := t.rowSize(tblTypeDef)
	off := t.tableOff[tblTypeDef] + (i-1)*tdRS
	name := t.str(readIndex(t.data, off+4, t.strSize))

	if outer, ok := t.nested[i]; ok {
		parent, err := t.FullTypeName(outer)
		if err != nil {
			return "", err
		}
		return parent + "+" + name, nil
	}
	ns := t.str(readIndex(t.data, off+4+t.strSize, t.strSize))
	return qualified(ns, name), nil
}

// blobInt decodes the Constant blob for a field. Only the integer widths the
// game's enums use are handled.
func (t *File) blobInt(blobIdx, ctype int) (int64, bool) {
	if blobIdx == 0 || t.blobOff == 0 {
		return 0, false
	}
	n, size := readCompressedUint(t.data, t.blobOff+blobIdx)
	start := t.blobOff + blobIdx + size
	if start+n > len(t.data) {
		return 0, false
	}
	raw := t.data[start : start+n]

	switch ctype {
	case 0x02: // Boolean
		if len(raw) >= 1 {
			return int64(raw[0]), true
		}
	case 0x03: // Char (u16)
		if len(raw) >= 2 {
			return int64(binary.LittleEndian.Uint16(raw)), true
		}
	case 0x04: // SByte
		if len(raw) >= 1 {
			return int64(int8(raw[0])), true
		}
	case 0x05: // Byte
		if len(raw) >= 1 {
			return int64(raw[0]), true
		}
	case 0x06: // Int16
		if len(raw) >= 2 {
			return int64(int16(binary.LittleEndian.Uint16(raw))), true
		}
	case 0x07: // UInt16
		if len(raw) >= 2 {
			return int64(binary.LittleEndian.Uint16(raw)), true
		}
	case 0x08: // Int32
		if len(raw) >= 4 {
			return int64(int32(binary.LittleEndian.Uint32(raw))), true
		}
	case 0x09: // UInt32
		if len(raw) >= 4 {
			return int64(binary.LittleEndian.Uint32(raw)), true
		}
	case 0x0A: // Int64
		if len(raw) >= 8 {
			return int64(binary.LittleEndian.Uint64(raw)), true
		}
	case 0x0B: // UInt64
		if len(raw) >= 8 {
			return int64(binary.LittleEndian.Uint64(raw)), true
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func readIndex(data []byte, off, size int) int {
	if size == 4 {
		return int(binary.LittleEndian.Uint32(data[off:]))
	}
	return int(binary.LittleEndian.Uint16(data[off:]))
}

func readCString(data []byte, off int) (string, int) {
	for i := off; i < len(data); i++ {
		if data[i] == 0 {
			return string(data[off:i]), i + 1
		}
	}
	return string(data[off:]), len(data)
}

func readCompressedUint(data []byte, off int) (int, int) {
	if off >= len(data) {
		return 0, 1
	}
	b0 := data[off]
	switch {
	case b0&0x80 == 0:
		return int(b0), 1
	case b0&0x40 == 0:
		if off+1 >= len(data) {
			return 0, 2
		}
		return int(b0&0x3F)<<8 | int(data[off+1]), 2
	default:
		if off+3 >= len(data) {
			return 0, 4
		}
		return int(b0&0x1F)<<24 | int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3]), 4
	}
}

func align4(n int) int { return (n + 3) &^ 3 }

// ---------------------------------------------------------------------------
// Public entry points
// ---------------------------------------------------------------------------

// Open reads the .NET assembly at path.
func Open(path string) (*File, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return openTables(f, path)
}

// TypeCount returns the number of TypeDef rows.
func (t *File) TypeCount() int { return int(t.rows[tblTypeDef]) }

// BaseName returns the base type name of the TypeDef at 1-based index i,
// resolving nested/qualified names. It returns "" for unspecified bases.
func (t *File) BaseName(i int) string {
	if i < 1 || i > int(t.rows[tblTypeDef]) {
		return ""
	}
	tdRS, _ := t.rowSize(tblTypeDef)
	tdr := t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2)
	off := t.tableOff[tblTypeDef] + (i-1)*tdRS
	ext := readIndex(t.data, off+4+2*t.strSize, tdr)
	switch ext & 0x3 {
	case 0:
		if n, err := t.FullTypeName(ext >> 2); err == nil {
			return n
		}
	case 1:
		if ns, name, err := t.typeRef(ext >> 2); err == nil {
			return qualified(ns, name)
		}
	}
	return ""
}

// Field is one field of a type.
type Field struct {
	Name      string
	Flags     uint16
	Signature string // method/field signature blob, hex
}

// Method is one method of a type.
type Method struct {
	Name      string
	Flags     uint16
	Params    []string
	Signature string // signature blob, hex
}

// Members returns the fields and methods declared by the TypeDef at 1-based
// index i.
func (t *File) Members(i int) ([]Field, []Method, error) {
	if i < 1 || i > int(t.rows[tblTypeDef]) {
		return nil, nil, fmt.Errorf("TypeDef index %d out of range", i)
	}
	tdRS, _ := t.rowSize(tblTypeDef)
	tdr := t.codedIndex([]int{tblTypeDef, tblTypeRef, tblTypeSpec}, 2)
	off := t.tableOff[tblTypeDef] + (i-1)*tdRS
	fieldOff := off + 4 + 2*t.strSize + tdr
	fieldList := readIndex(t.data, fieldOff, t.tableIndex(tblField))
	methodList := readIndex(t.data, fieldOff+t.tableIndex(tblField), t.tableIndex(tblMethodDef))

	var fieldEnd, methodEnd int
	if i < int(t.rows[tblTypeDef]) {
		noff := t.tableOff[tblTypeDef] + i*tdRS
		fieldEnd = readIndex(t.data, noff+4+2*t.strSize+tdr, t.tableIndex(tblField))
		methodEnd = readIndex(t.data, noff+4+2*t.strSize+tdr+t.tableIndex(tblField), t.tableIndex(tblMethodDef))
	} else {
		fieldEnd = int(t.rows[tblField]) + 1
		methodEnd = int(t.rows[tblMethodDef]) + 1
	}

	fieldRS, _ := t.rowSize(tblField)
	var fields []Field
	for j := fieldList; j < fieldEnd && j <= int(t.rows[tblField]); j++ {
		fo := t.tableOff[tblField] + (j-1)*fieldRS
		fields = append(fields, Field{
			Name:      t.str(readIndex(t.data, fo+2, t.strSize)),
			Flags:     uint16(t.u16(fo)),
			Signature: t.blobHex(readIndex(t.data, fo+2+t.strSize, t.blobSize)),
		})
	}

	methodRS, _ := t.rowSize(tblMethodDef)
	paramRS, _ := t.rowSize(tblParam)
	paramOff := 8 + t.strSize + t.blobSize
	var methods []Method
	for j := methodList; j < methodEnd && j <= int(t.rows[tblMethodDef]); j++ {
		mo := t.tableOff[tblMethodDef] + (j-1)*methodRS
		pstart := readIndex(t.data, mo+paramOff, t.tableIndex(tblParam))
		pend := int(t.rows[tblParam]) + 1
		if j < int(t.rows[tblMethodDef]) {
			pend = readIndex(t.data, mo+methodRS+paramOff, t.tableIndex(tblParam))
		}
		var params []string
		for k := pstart; k < pend && k <= int(t.rows[tblParam]); k++ {
			po := t.tableOff[tblParam] + (k-1)*paramRS
			params = append(params, t.str(readIndex(t.data, po+4, t.strSize)))
		}
		methods = append(methods, Method{
			Name:      t.str(readIndex(t.data, mo+8, t.strSize)),
			Flags:     uint16(t.u16(mo + 6)),
			Params:    params,
			Signature: t.blobHex(readIndex(t.data, mo+8+t.strSize, t.blobSize)),
		})
	}
	return fields, methods, nil
}

// blobHex returns the hex bytes of a #Blob entry.
func (t *File) blobHex(blobIdx int) string {
	if blobIdx == 0 || t.blobOff == 0 {
		return ""
	}
	n, size := readCompressedUint(t.data, t.blobOff+blobIdx)
	start := t.blobOff + blobIdx + size
	if start+n > len(t.data) {
		return ""
	}
	return fmt.Sprintf("%x", t.data[start:start+n])
}
