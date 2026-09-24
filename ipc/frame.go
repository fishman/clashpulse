package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

var (
	errFrameTooLarge = errors.New("ipc: frame exceeds size limit")
	frameReadTimeout = 5 * time.Second
)

func readFrame(r io.Reader, dst any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	return readFrameBody(r, header, dst)
}

func readConnFrame(conn net.Conn, dst any) error {
	var header [4]byte
	n, err := conn.Read(header[:])
	if n == 0 {
		if err == nil {
			return io.ErrNoProgress
		}
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(frameReadTimeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{})
	if n < len(header) {
		if _, err := io.ReadFull(conn, header[n:]); err != nil {
			return err
		}
	}
	return readFrameBody(conn, header, dst)
}

func readFrameBody(r io.Reader, header [4]byte, dst any) error {
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxFrameSize {
		return errFrameTooLarge
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("ipc: multiple JSON values in frame")
		}
		return err
	}
	return nil
}

func writeFrame(w io.Writer, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > MaxFrameSize {
		return errFrameTooLarge
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if err := writeFull(w, header[:]); err != nil {
		return err
	}
	return writeFull(w, body)
}

func writeFull(w io.Writer, body []byte) error {
	for len(body) != 0 {
		n, err := w.Write(body)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		body = body[n:]
	}
	return nil
}
