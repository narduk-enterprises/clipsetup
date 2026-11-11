package espprov

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protowire"
)

// Security 1, device side, as ESP-IDF protocomm_security1 and ESPProvision's ESPSecurity1 do it:
//
//  1. Cmd0 carries the client's X25519 public key. The device makes a key pair and 16 random
//     bytes, computes the shared secret, XORs it with SHA-256(PoP) when a PoP is set, and answers
//     Resp0 with its public key and the random bytes.
//  2. The shared key is an AES-256-CTR key and the random bytes are the initial counter block.
//     There is ONE keystream for the whole session, used in processing order for both directions.
//  3. Cmd1 carries the device's public key encrypted by the client. The device decrypts it,
//     compares it in constant time, and answers Resp1 with the client's public key encrypted.
//  4. Every later request and response on every endpoint except proto-ver is XORed with the next
//     bytes of the same keystream.

// session.proto / sec1.proto field numbers.
const (
	fSessionSecVer protowire.Number = 2
	fSessionSec1   protowire.Number = 11

	fSec1Msg protowire.Number = 1
	fSec1SC0 protowire.Number = 20
	fSec1SR0 protowire.Number = 21
	fSec1SC1 protowire.Number = 22
	fSec1SR1 protowire.Number = 23

	fSC0ClientPubkey     protowire.Number = 1
	fSR0Status           protowire.Number = 1
	fSR0DevicePubkey     protowire.Number = 2
	fSR0DeviceRandom     protowire.Number = 3
	fSC1ClientVerifyData protowire.Number = 2
	fSR1Status           protowire.Number = 1
	fSR1DeviceVerifyData protowire.Number = 3
)

const (
	secScheme1 = 1

	sec1Command0  = 0
	sec1Response0 = 1
	sec1Command1  = 2
	sec1Response1 = 3

	keyLen    = 32
	randomLen = 16
)

// ErrSessionNotEstablished is returned for an encrypted endpoint before the handshake completes.
var ErrSessionNotEstablished = errors.New("espprov: session not established")

// ErrProofMismatch means the client's verifier did not decrypt to the device key: a wrong PoP, or
// a client that is not running the same session.
var ErrProofMismatch = errors.New("espprov: proof of possession did not match")

type sec1State int

const (
	sec1WaitCmd0 sec1State = iota
	sec1WaitCmd1
	sec1Done
)

// Security1 is one device-side session. It is not safe for concurrent use; Server serialises it.
type Security1 struct {
	pop    []byte
	rand   io.Reader
	state  sec1State
	devPub []byte
	cliPub []byte
	stream cipher.Stream
}

// NewSecurity1 starts a session that expects pop (empty means no PoP). rand supplies the device's
// private key (32 bytes) and random (16 bytes) for each handshake, in that order.
func NewSecurity1(pop []byte, rand io.Reader) *Security1 {
	return &Security1{pop: append([]byte(nil), pop...), rand: rand}
}

// Established reports whether the handshake has completed.
func (s *Security1) Established() bool { return s.state == sec1Done }

// Crypt XORs b with the next len(b) keystream bytes into a new slice. Encryption and decryption
// are the same operation in CTR mode.
func (s *Security1) Crypt(b []byte) ([]byte, error) {
	if s.state != sec1Done {
		return nil, ErrSessionNotEstablished
	}
	out := make([]byte, len(b))
	s.stream.XORKeyStream(out, b)
	return out, nil
}

// Handle answers one prov-session request.
func (s *Security1) Handle(req []byte) ([]byte, error) {
	sd, err := parse(req)
	if err != nil {
		return nil, err
	}
	if v := sd.uint(fSessionSecVer); v != secScheme1 {
		return nil, fmt.Errorf("espprov: session asks for security %d, this device runs security 1", v)
	}
	p, err := sd.msg(fSessionSec1)
	if err != nil {
		return nil, err
	}
	switch p.uint(fSec1Msg) {
	case sec1Command0:
		// A new Cmd0 always restarts the session: ESPProvision re-initialises on reconnect.
		sc0, err := p.msg(fSec1SC0)
		if err != nil {
			return nil, err
		}
		return s.cmd0(sc0.bytes(fSC0ClientPubkey))
	case sec1Command1:
		if s.state != sec1WaitCmd1 {
			s.reset()
			return nil, fmt.Errorf("espprov: session command 1 out of order")
		}
		sc1, err := p.msg(fSec1SC1)
		if err != nil {
			return nil, err
		}
		return s.cmd1(sc1.bytes(fSC1ClientVerifyData))
	default:
		return nil, fmt.Errorf("espprov: unexpected security 1 message type %d", p.uint(fSec1Msg))
	}
}

func (s *Security1) reset() {
	s.state, s.devPub, s.cliPub, s.stream = sec1WaitCmd0, nil, nil, nil
}

func (s *Security1) cmd0(cliPub []byte) ([]byte, error) {
	s.reset()
	if len(cliPub) != keyLen {
		return nil, fmt.Errorf("espprov: client public key is %d bytes, want %d", len(cliPub), keyLen)
	}
	// The private key comes straight from rand (X25519 clamps it), so a fixed reader gives a
	// fixed transcript for the golden tests.
	var raw [keyLen]byte
	if _, err := io.ReadFull(s.rand, raw[:]); err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw[:])
	if err != nil {
		return nil, err
	}
	peer, err := ecdh.X25519().NewPublicKey(cliPub)
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(peer) // refuses low-order points (all-zero secret)
	if err != nil {
		return nil, err
	}
	if len(s.pop) > 0 {
		h := sha256.Sum256(s.pop)
		for i := range shared {
			shared[i] ^= h[i]
		}
	}
	random := make([]byte, randomLen)
	if _, err := io.ReadFull(s.rand, random); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return nil, err
	}
	s.stream = cipher.NewCTR(block, random)
	s.devPub = priv.PublicKey().Bytes()
	s.cliPub = append([]byte(nil), cliPub...)
	s.state = sec1WaitCmd1

	sr0 := enc(nil).varint(fSR0Status, uint64(StatusSuccess)).
		bytes(fSR0DevicePubkey, s.devPub).
		bytes(fSR0DeviceRandom, random)
	return sessionResponse(sec1Response0, fSec1SR0, sr0), nil
}

func (s *Security1) cmd1(verify []byte) ([]byte, error) {
	if len(verify) != keyLen {
		s.reset()
		return nil, fmt.Errorf("espprov: client verifier is %d bytes, want %d", len(verify), keyLen)
	}
	got := make([]byte, keyLen)
	s.stream.XORKeyStream(got, verify)
	if subtle.ConstantTimeCompare(got, s.devPub) != 1 {
		s.reset()
		return nil, ErrProofMismatch
	}
	devVerify := make([]byte, keyLen)
	s.stream.XORKeyStream(devVerify, s.cliPub)
	s.state = sec1Done

	sr1 := enc(nil).varint(fSR1Status, uint64(StatusSuccess)).bytes(fSR1DeviceVerifyData, devVerify)
	return sessionResponse(sec1Response1, fSec1SR1, sr1), nil
}

func sessionResponse(msgType uint64, field protowire.Number, body enc) []byte {
	p := enc(nil).varint(fSec1Msg, msgType).msg(field, body)
	return enc(nil).varint(fSessionSecVer, secScheme1).msg(fSessionSec1, p)
}
