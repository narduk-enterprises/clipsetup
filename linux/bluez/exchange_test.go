package bluez

import (
	"bytes"
	"errors"
	"testing"
)

func TestLongWriteThenLongRead(t *testing.T) {
	e := newExchanges()
	calls := 0
	echo := func(req []byte) ([]byte, error) {
		calls++
		return append([]byte("re:"), req...), nil
	}
	// A prepared write replayed by BlueZ as three WriteValue calls with offsets.
	for _, w := range []struct {
		off int
		b   string
	}{{0, "hello "}, {6, "long "}, {11, "request"}} {
		if err := e.write("dev", w.off, []byte(w.b)); err != nil {
			t.Fatal(err)
		}
	}
	// Reads with offsets, as a central does for a value longer than the MTU.
	var got []byte
	for off := 0; ; off += 5 {
		part, err := e.read("dev", off, echo)
		if err != nil {
			t.Fatal(err)
		}
		if len(part) > 5 {
			part = part[:5]
		}
		got = append(got, part...)
		if len(part) < 5 {
			break
		}
	}
	if string(got) != "re:hello long request" || calls != 1 {
		t.Fatalf("got %q after %d calls", got, calls)
	}
	// Re-reading offset 0 returns the same response without processing again.
	if again, _ := e.read("dev", 0, echo); string(again) != "re:hello long request" || calls != 1 {
		t.Fatalf("re-read %q, %d calls", again, calls)
	}
}

func TestBadOffsetsAndPeers(t *testing.T) {
	e := newExchanges()
	if err := e.write("dev", 3, []byte("x")); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("offset write with nothing pending: %v", err)
	}
	_ = e.write("dev", 0, []byte("abc"))
	if err := e.write("dev", 9, []byte("x")); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("offset past the data: %v", err)
	}
	if _, err := e.read("dev", 99, func(b []byte) ([]byte, error) { return b, nil }); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("read past the response: %v", err)
	}
	// Each central has its own request.
	_ = e.write("a", 0, []byte("from a"))
	_ = e.write("b", 0, []byte("from b"))
	ra, _ := e.read("a", 0, func(b []byte) ([]byte, error) { return b, nil })
	rb, _ := e.read("b", 0, func(b []byte) ([]byte, error) { return b, nil })
	if string(ra) != "from a" || string(rb) != "from b" {
		t.Fatalf("a=%q b=%q", ra, rb)
	}
	if !e.forget("a") || e.forget("a") {
		t.Fatal("forget did not report state once")
	}
	if err := e.write("dev", 0, bytes.Repeat([]byte{1}, maxRequest+1)); err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestHandlerErrorLeavesNoResponse(t *testing.T) {
	e := newExchanges()
	_ = e.write("dev", 0, []byte("req"))
	if _, err := e.read("dev", 0, func([]byte) ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("handler error swallowed")
	}
	if got, err := e.read("dev", 0, func([]byte) ([]byte, error) { t.Fatal("reprocessed"); return nil, nil }); err != nil || len(got) != 0 {
		t.Fatalf("after an error: %q, %v", got, err)
	}
}
