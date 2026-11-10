package espprov

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// Security1Client is the phone's half of security 1, written from ESPProvision's ESPSecurity1.swift
// (and ESP-IDF's esp_prov security1.py, which does the same). The device never needs it; it exists
// so tests and the bench probe can drive a Server the way the App Clip does.
type Security1Client struct {
	pop    []byte
	priv   *ecdh.PrivateKey
	stream cipher.Stream
	devPub []byte
	done   bool
}

// NewSecurity1Client makes a client key pair from 32 bytes of rand.
func NewSecurity1Client(pop []byte, rand io.Reader) (*Security1Client, error) {
	var raw [keyLen]byte
	if _, err := io.ReadFull(rand, raw[:]); err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw[:])
	if err != nil {
		return nil, err
	}
	return &Security1Client{pop: append([]byte(nil), pop...), priv: priv}, nil
}

// Cmd0 is the first prov-session request. Like ESPProvision it leaves msg at its zero value.
func (c *Security1Client) Cmd0() []byte {
	sc0 := enc(nil).bytes(fSC0ClientPubkey, c.priv.PublicKey().Bytes())
	p := enc(nil).msg(fSec1SC0, sc0)
	return enc(nil).varint(fSessionSecVer, secScheme1).msg(fSessionSec1, p)
}

// Cmd1 reads Resp0 and returns the second prov-session request.
func (c *Security1Client) Cmd1(resp0 []byte) ([]byte, error) {
	sd, err := parse(resp0)
	if err != nil {
		return nil, err
	}
	if sd.uint(fSessionSecVer) != secScheme1 {
		return nil, errors.New("espprov: security version mismatch")
	}
	p, err := sd.msg(fSessionSec1)
	if err != nil {
		return nil, err
	}
	sr0, err := p.msg(fSec1SR0)
	if err != nil {
		return nil, err
	}
	c.devPub = sr0.bytes(fSR0DevicePubkey)
	peer, err := ecdh.X25519().NewPublicKey(c.devPub)
	if err != nil {
		return nil, err
	}
	shared, err := c.priv.ECDH(peer)
	if err != nil {
		return nil, err
	}
	if len(c.pop) > 0 {
		h := sha256.Sum256(c.pop)
		for i := range shared {
			shared[i] ^= h[i]
		}
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return nil, err
	}
	random := sr0.bytes(fSR0DeviceRandom)
	if len(random) != aes.BlockSize {
		return nil, fmt.Errorf("espprov: device random is %d bytes", len(random))
	}
	c.stream = cipher.NewCTR(block, random)
	verify := make([]byte, keyLen)
	c.stream.XORKeyStream(verify, c.devPub)
	sc1 := enc(nil).bytes(fSC1ClientVerifyData, verify)
	p1 := enc(nil).varint(fSec1Msg, sec1Command1).msg(fSec1SC1, sc1)
	return enc(nil).varint(fSessionSecVer, secScheme1).msg(fSessionSec1, p1), nil
}

// Finish reads Resp1 and checks the device's proof.
func (c *Security1Client) Finish(resp1 []byte) error {
	sd, err := parse(resp1)
	if err != nil {
		return err
	}
	p, err := sd.msg(fSessionSec1)
	if err != nil {
		return err
	}
	sr1, err := p.msg(fSec1SR1)
	if err != nil {
		return err
	}
	got := make([]byte, keyLen)
	c.stream.XORKeyStream(got, sr1.bytes(fSR1DeviceVerifyData))
	if !bytes.Equal(got, c.priv.PublicKey().Bytes()) {
		return errors.New("espprov: device proof did not match")
	}
	c.done = true
	return nil
}

// Crypt encrypts a request or decrypts a response on the session keystream.
func (c *Security1Client) Crypt(b []byte) []byte {
	out := make([]byte, len(b))
	c.stream.XORKeyStream(out, b)
	return out
}
