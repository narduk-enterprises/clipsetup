package espprov

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// The protocol's messages are small, so they are encoded by hand with protowire instead of
// generated code. Field numbers and types are from ESP-IDF's .proto files (protocomm: session,
// sec1, constants; wifi_provisioning: wifi_scan, wifi_config, wifi_constants), which ESPProvision's
// network_*.pb.swift mirror on the wire.

// Status is constants.proto Status.
type Status uint64

const (
	StatusSuccess          Status = 0
	StatusInvalidSecScheme Status = 1
	StatusInvalidProto     Status = 2
	StatusTooManySessions  Status = 3
	StatusInvalidArgument  Status = 4
	StatusInternalError    Status = 5
	StatusCryptoError      Status = 6
	StatusInvalidSession   Status = 7
)

var errMalformed = errors.New("espprov: malformed protobuf")

type field struct {
	typ protowire.Type
	v   uint64
	b   []byte
}

// fields is a decoded message: every occurrence of every field number, in order.
type fields map[protowire.Number][]field

func parse(b []byte) (fields, error) {
	f := fields{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, fmt.Errorf("%w: %v", errMalformed, protowire.ParseError(n))
		}
		b = b[n:]
		var fl field
		fl.typ = typ
		switch typ {
		case protowire.VarintType:
			fl.v, n = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			fl.b, n = protowire.ConsumeBytes(b)
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
		}
		if n < 0 {
			return nil, fmt.Errorf("%w: %v", errMalformed, protowire.ParseError(n))
		}
		b = b[n:]
		f[num] = append(f[num], fl)
	}
	return f, nil
}

func (f fields) last(n protowire.Number) (field, bool) {
	l := f[n]
	if len(l) == 0 {
		return field{}, false
	}
	return l[len(l)-1], true
}

func (f fields) has(n protowire.Number) bool { _, ok := f.last(n); return ok }

func (f fields) uint(n protowire.Number) uint64 {
	if fl, ok := f.last(n); ok && fl.typ == protowire.VarintType {
		return fl.v
	}
	return 0
}

func (f fields) bytes(n protowire.Number) []byte {
	if fl, ok := f.last(n); ok && fl.typ == protowire.BytesType {
		return fl.b
	}
	return nil
}

// msg decodes an embedded message. A missing field decodes as an empty message, as proto3 does.
func (f fields) msg(n protowire.Number) (fields, error) {
	fl, ok := f.last(n)
	if !ok {
		return fields{}, nil
	}
	if fl.typ != protowire.BytesType {
		return nil, fmt.Errorf("%w: field %d is not a message", errMalformed, n)
	}
	return parse(fl.b)
}

// enc builds a message. Scalars equal to their zero value are omitted, as proto3 does, except
// where the caller uses the *Always variants for a set oneof member.
type enc []byte

func (e enc) varint(n protowire.Number, v uint64) enc {
	if v == 0 {
		return e
	}
	return e.varintAlways(n, v)
}

func (e enc) varintAlways(n protowire.Number, v uint64) enc {
	e = protowire.AppendTag(e, n, protowire.VarintType)
	return protowire.AppendVarint(e, v)
}

// int32 encodes a proto int32: negative values are sign-extended to 64 bits (10 bytes), not
// zigzagged.
func (e enc) int32(n protowire.Number, v int32) enc { return e.varint(n, uint64(int64(v))) }

func (e enc) bool(n protowire.Number, v bool) enc {
	if !v {
		return e
	}
	return e.varintAlways(n, 1)
}

func (e enc) bytes(n protowire.Number, b []byte) enc {
	if len(b) == 0 {
		return e
	}
	return e.bytesAlways(n, b)
}

func (e enc) bytesAlways(n protowire.Number, b []byte) enc {
	e = protowire.AppendTag(e, n, protowire.BytesType)
	return protowire.AppendBytes(e, b)
}

// msg embeds a message. It is always written, even when empty, because every embedded message in
// this protocol is a oneof member and a set oneof member is always on the wire.
func (e enc) msg(n protowire.Number, m enc) enc { return e.bytesAlways(n, m) }
