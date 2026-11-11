package value

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// roundTrip is the definition of the view: the value encoded and decoded
// into any. The tests compare with encoding/json as the toolchain builds it;
// run them also with GOEXPERIMENT=nojsonv2 (or jsonv2) to cover both
// implementations. Malformed tag names and nil maps of unsupported key types
// are left out: the two implementations disagree there.
func roundTrip(v any) (any, error) {
	data, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type (
	inner struct {
		A int    `json:"a"`
		B string `json:"b,omitempty"`
	}
	hidden struct {
		Exported string
		private  string
	}
	Embedded struct {
		E1 int
		E2 string `json:"e2"`
	}
	Other struct {
		E1 int
		X  bool
	}
	Tagged struct {
		E1 int `json:"E1"`
	}
	textKey  struct{ a, b int }
	textVal  int
	ptrText  string
	jsonVal  struct{ N int }
	ptrJSON  struct{ N int }
	zeroer   struct{ V int }
	ptrZero  struct{ V int }
	myBytes  []byte
	myByte   uint8
	textByte uint8
	mixed    struct {
		Embedded
		*Other
		Tagged
		hidden
		*inner         `json:"inner"`
		Name           string            `json:"name"`
		Skip           string            `json:"-"`
		Dash           string            `json:"-,"`
		Empty          string            `json:",omitempty"`
		Zero           int               `json:"zero,omitempty"`
		OmitZero       time.Time         `json:"oz,omitzero"`
		OmitZero2      zeroer            `json:"oz2,omitzero"`
		OmitZero3      ptrZero           `json:"oz3,omitzero"`
		OmitZero4      *ptrZero          `json:"oz4,omitzero"`
		OmitZero5      inner             `json:"oz5,omitzero"`
		Quoted         int               `json:"q,string"`
		QuotedF        float64           `json:"qf,string"`
		QuotedF32      float32           `json:"qf32,string"`
		QuotedBig      float64           `json:"qbig,string"`
		QuotedS        string            `json:"qs,string"`
		QuotedB        bool              `json:"qb,string"`
		QuotedP        *int              `json:"qp,string"`
		QuotedNil      *int              `json:"qnil,string"`
		QuotedT        textVal           `json:"qt,string"`
		QuotedN        json.Number       `json:"qn,string"`
		QuotedSlice    []int             `json:"qsl,string"`
		F32            float32           `json:"f32"`
		Neg0           float64           `json:"neg0"`
		Big            uint64            `json:"big"`
		Bytes          []byte            `json:"bytes"`
		NilBytes       []byte            `json:"nilBytes"`
		MyBytes        myBytes           `json:"myBytes"`
		MyByteSlice    []myByte          `json:"myByteSlice"`
		TextByteSlice  []textByte        `json:"textByteSlice"`
		ByteArray      [3]byte           `json:"byteArray"`
		Num            json.Number       `json:"num"`
		EmptyNum       json.Number       `json:"emptyNum"`
		Raw            json.RawMessage   `json:"raw"`
		NilRaw         json.RawMessage   `json:"nilRaw"`
		Text           textVal           `json:"text"`
		PtrText        ptrText           `json:"ptrText"`
		PtrTextP       *ptrText          `json:"ptrTextP"`
		JSON           jsonVal           `json:"json"`
		PtrJSON        ptrJSON           `json:"ptrJSON"`
		NilJSON        *jsonVal          `json:"nilJSON"`
		Iface          any               `json:"iface"`
		IfaceJSON      json.Marshaler    `json:"ifaceJSON"`
		TextKeys       map[textKey]int   `json:"textKeys"`
		IntKeys        map[int8]string   `json:"intKeys"`
		UintKeys       map[uint16]string `json:"uintKeys"`
		BadUTF8        string            `json:"badUTF8"`
		BadKey         map[string]int    `json:"badKey"`
		HTML           string            `json:"html"`
		Time           time.Time         `json:"time"`
		Duration       time.Duration     `json:"duration"`
		Addr           netip.Addr        `json:"addr"`
		Nested         [][]map[string]*inner
		unexported     int
		Arr            [2]inner
		PtrArr         *[2]int
		Interface      []any
		EmptyIface     any `json:"emptyIface,omitempty"`
		Punct          int `json:"a-b.c"`
		NoneTag        int `json:",string"`
		Ünïcode        int
		AnonymousField struct{ X, Y int }
	}
)

func (k textKey) MarshalText() ([]byte, error) {
	return []byte(strings.Repeat("k", k.a) + "-" + strings.Repeat("v", k.b)), nil
}
func (t textVal) MarshalText() ([]byte, error) {
	return []byte("text:" + string(rune('a'+int(t)))), nil
}
func (t *ptrText) MarshalText() ([]byte, error) { return []byte("ptr:" + string(*t)), nil }
func (j jsonVal) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"n": j.N, "list": []int{j.N}})
}
func (j *ptrJSON) MarshalJSON() ([]byte, error) { return []byte(` { "p" : 1.50 } `), nil }
func (z zeroer) IsZero() bool                   { return z.V == 7 }
func (z *ptrZero) IsZero() bool                 { return z.V == 7 }
func (b textByte) MarshalText() ([]byte, error) { return []byte{'b', byte(b)}, nil }

func sample(r *rand.Rand) mixed {
	n := r.IntN(5)
	m := mixed{
		Embedded:      Embedded{E1: r.IntN(9), E2: "e"},
		Name:          "name",
		Skip:          "skip",
		Dash:          "dash",
		Zero:          r.IntN(2),
		OmitZero2:     zeroer{V: 6 + r.IntN(2)},
		OmitZero3:     ptrZero{V: 6 + r.IntN(2)},
		Quoted:        -r.IntN(100),
		QuotedF:       []float64{0, 1.5, 1e21, 1e-7, 123456789.125, -2e-9}[r.IntN(6)],
		QuotedF32:     float32(r.Float64()),
		QuotedBig:     1e300,
		QuotedS:       "a \"q\" <b>  ",
		QuotedB:       r.IntN(2) == 0,
		QuotedT:       textVal(r.IntN(3)),
		QuotedN:       "12.5e3",
		QuotedSlice:   []int{1, 2},
		F32:           float32(r.Float64() * 1000),
		Neg0:          math.Copysign(0, -1),
		Big:           1<<64 - 1 - uint64(r.IntN(1000)),
		Bytes:         []byte("hello\x00\xff"),
		MyBytes:       myBytes{1, 2, 3},
		MyByteSlice:   []myByte{4, 5},
		TextByteSlice: []textByte{'x', 'y'},
		ByteArray:     [3]byte{7, 8, 9},
		Num:           json.Number("-0.25e-2"),
		Raw:           json.RawMessage(`{"z": [1, {"y": null}], "a": "é"}`),
		Text:          textVal(r.IntN(3)),
		PtrText:       "p",
		JSON:          jsonVal{N: r.IntN(9)},
		PtrJSON:       ptrJSON{N: 1},
		Iface:         []any{1, "x", nil, map[string]any{"k": jsonVal{N: 2}}, ptrText("i")},
		IfaceJSON:     jsonVal{N: 4},
		TextKeys:      map[textKey]int{{1, 2}: 1, {3, 0}: 2},
		IntKeys:       map[int8]string{-3: "m", 4: "p"},
		UintKeys:      map[uint16]string{65535: "max"},
		BadUTF8:       "a\xffb\xc3\x28c\xed\xa0\x80",
		BadKey:        map[string]int{"\xfe": 1, "ok": 2},
		HTML:          "<script>&</script>",
		Time:          time.Date(2026, 11, 11, 10, 0, 0, 123, time.FixedZone("X", 5*3600)),
		Duration:      time.Duration(r.IntN(1e9)),
		Addr:          netip.MustParseAddr("192.0.2.1"),
		Nested:        [][]map[string]*inner{{{"a": {A: 1}, "nil": nil}}, nil, {}},
		unexported:    5,
		Arr:           [2]inner{{A: 1, B: "b"}, {}},
		PtrArr:        &[2]int{1, 2},
		Interface:     []any{int8(-1), uint(3), float32(0.1), true, []string(nil), map[string]any(nil)},
		Punct:         1,
		NoneTag:       2,
		Ünïcode:       3,
	}
	m.AnonymousField.X = n
	m.hidden.Exported = "promoted"
	if n > 1 {
		m.Other = &Other{E1: 9, X: true}
		m.inner = &inner{A: n}
		pt := ptrText("pp")
		m.PtrTextP = &pt
		q := n
		m.QuotedP = &q
		m.OmitZero = time.Unix(int64(n), 0).UTC()
		m.OmitZero4 = &ptrZero{V: 6 + n%2}
	}
	return m
}

func TestViewMatchesRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 200 {
		m := sample(r)
		for _, v := range []any{m, &m, []mixed{m}, map[string]any{"m": &m}, []*mixed{&m, nil}} {
			want, err := roundTrip(v)
			if err != nil {
				t.Fatalf("round trip %d: %v", i, err)
			}
			got, err := FromGo(v)
			if err != nil {
				t.Fatalf("view %d: %v", i, err)
			}
			if !reflect.DeepEqual(got, want) {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(want)
				t.Fatalf("view %d of %T differs:\n got %s\nwant %s", i, v, gj, wj)
			}
		}
	}
}

func TestViewScalars(t *testing.T) {
	values := []any{
		nil, true, 0, -1, int64(math.MinInt64), uint64(math.MaxUint64), int64(1<<53 + 1),
		0.1, float32(0.1), float32(16777217), math.MaxFloat64, math.SmallestNonzeroFloat64, float32(math.MaxFloat32),
		"", "é", "\x80", "\U0010FFFF", json.Number("1e308"), []byte{}, [0]int{}, struct{}{},
		map[string]any{}, []any{}, (*int)(nil), any(nil), time.Time{}, textVal(2), ptrText("x"), jsonVal{},
		&jsonVal{N: 3}, (*jsonVal)(nil), (*ptrJSON)(nil),
	}
	for _, v := range values {
		want, werr := roundTrip(v)
		got, gerr := FromGo(v)
		if (werr != nil) != (gerr != nil) {
			t.Errorf("%T %#v: round trip error %v, view error %v", v, v, werr, gerr)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%T %#v: view %#v, round trip %#v", v, v, got, want)
		}
	}
}

type node struct {
	Next *node `json:"next"`
}

func TestViewErrors(t *testing.T) {
	loop := &node{}
	loop.Next = loop
	selfMap := map[string]any{}
	selfMap["me"] = selfMap
	selfSlice := []any{nil}
	selfSlice[0] = selfSlice
	for name, v := range map[string]any{
		"NaN":             math.NaN(),
		"float32 Inf":     float32(math.Inf(-1)),
		"channel":         make(chan int),
		"function":        func() {},
		"complex":         complex(1, 2),
		"bool keys":       map[bool]int{true: 1},
		"nil func field":  struct{ F func() }{},
		"pointer cycle":   loop,
		"map cycle":       selfMap,
		"slice cycle":     selfSlice,
		"bad number":      json.Number("1x"),
		"bad raw":         json.RawMessage(`{`),
		"failing marshal": failing{},
	} {
		if _, err := roundTrip(v); err == nil {
			t.Errorf("%s: the round trip accepts it; the test is wrong", name)
		}
		_, err := FromGo(v)
		if err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A shared pointer that is not a cycle is fine.
	shared := &inner{A: 1}
	if _, err := FromGo([]*inner{shared, shared}); err != nil {
		t.Errorf("shared pointer: %v", err)
	}
}

type failing struct{}

func (failing) MarshalJSON() ([]byte, error) { return nil, errors.New("boom") }

type benchItem struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Author   struct{ Name, Initials, Color string }
	Tags     []struct{ Name, Color string } `json:"tags"`
	Score    int                            `json:"score"`
	Comments int                            `json:"comments"`
	Done     bool                           `json:"done"`
	Date     string                         `json:"date"`
}

func benchData() any {
	items := make([]benchItem, 1000)
	for i := range items {
		items[i] = benchItem{ID: i, Title: "long red pizza", Score: i % 1000, Date: "Oct 12, 2026"}
		items[i].Author.Name = "Donald Knuth"
		items[i].Tags = []struct{ Name, Color string }{{"perf", "#fff"}, {"ui", "#eee"}}
	}
	return struct {
		Items []benchItem `json:"items"`
	}{items}
}

func BenchmarkFromGo(b *testing.B) {
	v := benchData()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := FromGo(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	v := benchData()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := roundTrip(v); err != nil {
			b.Fatal(err)
		}
	}
}
