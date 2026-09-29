package cover

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTransmitChunks(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 10000) // base64: 13336 bytes → 4 chunks
	seq := Transmit(42, data, 20, 10)
	parts := strings.Split(strings.TrimSuffix(seq, "\x1b\\"), "\x1b\\")
	if len(parts) != 4 {
		t.Fatalf("chunks = %d", len(parts))
	}
	if !strings.HasPrefix(parts[0], "\x1b_Ga=T,U=1,f=100,t=d,i=42,c=20,r=10,q=2,m=1;") {
		t.Fatalf("first chunk header: %q", parts[0][:60])
	}
	if !strings.HasPrefix(parts[3], "\x1b_Gm=0,q=2;") || !strings.HasPrefix(parts[1], "\x1b_Gm=1,q=2;") {
		t.Fatal("continuation headers wrong")
	}
	var payload strings.Builder
	for _, p := range parts {
		payload.WriteString(p[strings.IndexByte(p, ';')+1:])
	}
	got, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("payload does not round-trip")
	}
	if Delete(42) != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
		t.Fatal("delete sequence")
	}
}

func TestPlaceholders(t *testing.T) {
	lines := Placeholders(3, 2)
	if len(lines) != 2 {
		t.Fatal("rows")
	}
	want := string([]rune{placeholder, diacritics[1], diacritics[0], placeholder, diacritics[1], diacritics[1], placeholder, diacritics[1], diacritics[2]})
	if lines[1] != want {
		t.Fatalf("row 1 = %q, want %q", lines[1], want)
	}
}

func TestBox(t *testing.T) {
	m := New(t.TempDir(), 10, 20)
	if c, r, ok := m.Box(42, 60); !ok || c != 40 || r != 20 { // square in pixels: 40×10 = 20×20
		t.Fatalf("box = %d×%d %v", c, r, ok)
	}
	if c, r, ok := m.Box(42, 20); !ok || r != 10 || c != 20 { // capped at half the height
		t.Fatalf("capped box = %d×%d %v", c, r, ok)
	}
	if _, _, ok := m.Box(42, 6); ok {
		t.Fatal("too small a box accepted")
	}
}

func jpegServer(t *testing.T, hits *int32) *httptest.Server {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	for y := range 1000 {
		for x := range 1000 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.URL.Path == "/missing.jpg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
}

func TestLoadPlaceEvict(t *testing.T) {
	var hits int32
	srv := jpegServer(t, &hits)
	defer srv.Close()
	dir := t.TempDir()
	m := New(dir, 10, 20)
	ctx := context.Background()
	url := srv.URL + "/a.jpg"
	if err := m.Load(ctx, url); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(m.pngs[url]))
	if err != nil || img.Bounds().Dx() != maxSide || img.Bounds().Dy() != maxSide {
		t.Fatalf("stored cover: %v %v", img.Bounds(), err)
	}
	// A second manager finds it in the disk cache.
	m2 := New(dir, 10, 20)
	if err := m2.Load(ctx, url); err != nil || hits != 1 {
		t.Fatalf("disk cache not used (hits=%d, err=%v)", hits, err)
	}
	if err := m.Load(ctx, srv.URL+"/missing.jpg"); err == nil || !m.Failed(srv.URL+"/missing.jpg") {
		t.Fatal("failed load not recorded")
	}

	id, seq := m.Place(url, 20, 10)
	if id < firstID || !strings.Contains(seq, "a=T,U=1") {
		t.Fatalf("first placement: id=%d", id)
	}
	if id2, seq2 := m.Place(url, 20, 10); id2 != id || seq2 != "" {
		t.Fatal("same placement sent twice")
	}
	// Fill up; the oldest placements are deleted and their ids reused.
	for i := range keep + 3 {
		_, seq = m.Place(url, 20, 11+i)
	}
	if len(m.placed) != keep || !strings.Contains(seq, "a=d,d=I") {
		t.Fatalf("placed=%d, eviction missing", len(m.placed))
	}
	if !strings.Contains(m.Cleanup(), "a=d,d=I") {
		t.Fatal("cleanup empty")
	}
}
