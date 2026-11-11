package espprov

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// The Cmd0 layout below is derived by hand from the .proto definitions, not from any library:
// SessionData{sec_ver(2)=1, sec1(11)=Sec1Payload{msg(1)=0 omitted, sc0(20)=SessionCmd0{client_pubkey(1)}}}
//
//	10 01            sec_ver = SecScheme1
//	5a 25            field 11, 37 bytes
//	a2 01 22         field 20 (two-byte tag), 34 bytes
//	0a 20 <32 bytes> field 1, 32 bytes
//
// ESP-IDF's esp_prov produced exactly this prefix in the golden transcript.
func TestCmd0WireFormat(t *testing.T) {
	c, err := NewSecurity1Client(nil, bytes.NewReader(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	got := c.Cmd0()
	want := append([]byte{0x10, 0x01, 0x5a, 0x25, 0xa2, 0x01, 0x22, 0x0a, 0x20}, c.priv.PublicKey().Bytes()...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Cmd0 = %x\nwant  %x", got, want)
	}
}

func handshake(t *testing.T, dev *Security1, clientPoP string) (*Security1Client, error) {
	t.Helper()
	c, err := NewSecurity1Client([]byte(clientPoP), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r0, err := dev.Handle(c.Cmd0())
	if err != nil {
		return nil, err
	}
	cmd1, err := c.Cmd1(r0)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := dev.Handle(cmd1)
	if err != nil {
		return nil, err
	}
	return c, c.Finish(r1)
}

func TestSecurity1RoundTripAndOneStream(t *testing.T) {
	for _, pop := range []string{"k3m9qrst", ""} {
		dev := NewSecurity1([]byte(pop), rand.Reader)
		c, err := handshake(t, dev, pop)
		if err != nil || !dev.Established() {
			t.Fatalf("pop %q: handshake: %v", pop, err)
		}
		// Requests and responses of different sizes, in order, on one keystream: each side must
		// stay in step even when a message ends mid-block.
		for i, msg := range []string{"a", "seventeen bytes!!", "", "a much longer message that spans several AES blocks"} {
			plain, err := dev.Crypt(c.Crypt([]byte(msg)))
			if err != nil || string(plain) != msg {
				t.Fatalf("pop %q request %d: got %q, %v", pop, i, plain, err)
			}
			reply := []byte("reply " + msg)
			enc, _ := dev.Crypt(reply)
			if got := c.Crypt(enc); !bytes.Equal(got, reply) {
				t.Fatalf("pop %q response %d: got %q", pop, i, got)
			}
		}
	}
}

func TestSecurity1WrongPoP(t *testing.T) {
	dev := NewSecurity1([]byte("k3m9qrst"), rand.Reader)
	if _, err := handshake(t, dev, "k3m9qrsu"); !errors.Is(err, ErrProofMismatch) {
		t.Fatalf("wrong PoP: err = %v, want ErrProofMismatch", err)
	}
	if dev.Established() {
		t.Fatal("session established with a wrong PoP")
	}
	if _, err := dev.Crypt([]byte("x")); !errors.Is(err, ErrSessionNotEstablished) {
		t.Fatalf("Crypt after a failed handshake: %v", err)
	}
	// A fresh Cmd0 starts over, as ESPProvision does on reconnect.
	if _, err := handshake(t, dev, "k3m9qrst"); err != nil {
		t.Fatalf("retry with the right PoP: %v", err)
	}
}

func TestSecurity1RejectsBadInput(t *testing.T) {
	dev := NewSecurity1([]byte("p"), rand.Reader)
	c, _ := NewSecurity1Client([]byte("p"), rand.Reader)
	r0, _ := NewSecurity1([]byte("p"), rand.Reader).Handle(c.Cmd0())
	cmd1, _ := c.Cmd1(r0)
	if _, err := dev.Handle(cmd1); err == nil {
		t.Error("Cmd1 before Cmd0 was accepted")
	}
	short := mustHex("10015a05a201020a00")
	if _, err := dev.Handle(short); err == nil {
		t.Error("an empty client key was accepted")
	}
	// The all-zero point has order 1: X25519 gives an all-zero secret, which must be refused.
	zero := append(mustHex("10015a25a20122"+"0a20"), make([]byte, 32)...)
	if _, err := dev.Handle(zero); err == nil {
		t.Error("a low-order client key was accepted")
	}
	sec2 := mustHex("1002")
	if _, err := dev.Handle(sec2); err == nil {
		t.Error("a security 2 request was accepted")
	}
	if _, err := dev.Handle([]byte{0xff}); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestSessionSurvivesHandlerErrors(t *testing.T) {
	s, _ := newTestServer(t, "pin")
	if _, err := s.Register("too-big", func(context.Context, string, []byte) ([]byte, error) {
		return make([]byte, MaxResponse+1), nil
	}); err != nil {
		t.Fatal(err)
	}
	c := &Client{T: direct(s, "p1")}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "pin", rand.Reader); err != nil {
		t.Fatal(err)
	}
	// A result page before any scan is an error, and so is a response over the attribute limit.
	// Neither encrypts a response, so both keystreams stay in step and the session goes on.
	page := enc(nil).varint(fScanMsg, scanCmdResult).msg(fScanCmdResult, enc(nil).varint(2, 4))
	if _, err := c.Send(ctx, EndpointScan, page); err == nil {
		t.Fatal("empty scan page answered")
	}
	if _, err := c.Send(ctx, "too-big", []byte("x")); err == nil {
		t.Fatal("oversized response sent")
	}
	if nets, err := c.ScanWiFi(ctx); err != nil || len(nets) != len(goldenNetworks()) {
		t.Fatalf("after handler errors: %d networks, %v", len(nets), err)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
