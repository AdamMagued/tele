package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// MaxLine is the longest line a decoder takes. The largest payload is a
// window of history: media crosses as paths, never bytes, so a line this long
// is no frame anybody meant to send.
const MaxLine = 8 << 20

// Encoder writes frames, one per line. It is safe for several goroutines: each
// frame goes out whole, so two writers never interleave inside a line, and in
// the order its writes were made, which is what keeps deltas in the order they
// were computed.
type Encoder struct {
	mu sync.Mutex
	w  io.Writer
}

func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode writes f as one line. A frame that has no shape is not written:
// sending one would end the connection on the other side.
func (e *Encoder) Encode(f Frame) error {
	if _, err := f.Shape(); err != nil {
		return err
	}
	line, err := json.Marshal(f)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = e.w.Write(line)
	return err
}

// Decoder reads frames, one per line. It is for one goroutine: the lines of a
// connection are read in order.
type Decoder struct {
	r *bufio.Reader
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReaderSize(r, 64<<10)}
}

// Decode reads the next frame. It answers io.EOF when the connection ended
// between frames, and ErrProtocol for a line that is not a frame; after
// either, nothing more is read.
func (d *Decoder) Decode() (Frame, error) {
	line, err := d.readLine()
	if err != nil {
		return Frame{}, err
	}
	var f Frame
	if err := strictUnmarshal(line, &f); err != nil {
		return Frame{}, protocolError("%v", err)
	}
	if _, err := f.Shape(); err != nil {
		return Frame{}, err
	}
	return f, nil
}

// readLine reads up to the next newline, refusing a line longer than MaxLine
// rather than buffering it whole.
func (d *Decoder) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := d.r.ReadSlice('\n')
		if len(line)+len(chunk) > MaxLine+1 {
			return nil, protocolError("a line longer than %d bytes", MaxLine)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, io.EOF
			}
			return nil, protocolError("the connection ended inside a line")
		default:
			return nil, err
		}
	}
}

// strictUnmarshal decodes data into v, refusing a key v does not have: a key
// the protocol does not know is a peer speaking another protocol.
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("more than one value on a line")
	}
	return nil
}
