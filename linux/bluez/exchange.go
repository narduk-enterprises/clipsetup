package bluez

import (
	"errors"
	"sync"
)

// ErrInvalidOffset is a write or read offset past the data.
var ErrInvalidOffset = errors.New("bluez: invalid offset")

// maxRequest bounds one request; the largest real one (a Wi-Fi config) is under 150 bytes.
const maxRequest = 4096

// exchanges holds, per central, the request being written and the last response.
type exchanges struct {
	mu    sync.Mutex
	peers map[string]*exchange
}

type exchange struct {
	req     []byte
	pending bool // req has been written and not yet processed
	resp    []byte
}

func newExchanges() *exchanges { return &exchanges{peers: map[string]*exchange{}} }

func (e *exchanges) get(peer string) *exchange {
	x, ok := e.peers[peer]
	if !ok {
		x = &exchange{}
		e.peers[peer] = x
	}
	return x
}

// write stores value at offset. Offset 0 starts a new request; a later offset must continue it.
func (e *exchanges) write(peer string, offset int, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	x := e.get(peer)
	if offset == 0 {
		x.req = x.req[:0]
	} else if !x.pending || offset > len(x.req) {
		return ErrInvalidOffset
	}
	if offset+len(value) > maxRequest {
		return errors.New("bluez: request too long")
	}
	x.req = append(x.req[:offset], value...)
	x.pending = true
	return nil
}

// read returns the response from offset, running handle on the pending request first when the
// read starts at offset 0. The lock is held across handle so a peer's requests are processed in
// the order they were written, which keeps the session keystream in step.
func (e *exchanges) read(peer string, offset int, handle func([]byte) ([]byte, error)) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x := e.get(peer)
	if offset == 0 && x.pending {
		req := append([]byte(nil), x.req...)
		x.pending, x.req, x.resp = false, x.req[:0], nil
		resp, err := handle(req)
		if err != nil {
			return nil, err
		}
		x.resp = resp
	}
	if offset > len(x.resp) {
		return nil, ErrInvalidOffset
	}
	return x.resp[offset:], nil
}

// forget drops a peer's state and reports whether it had any.
func (e *exchanges) forget(peer string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.peers[peer]
	delete(e.peers, peer)
	return ok
}
