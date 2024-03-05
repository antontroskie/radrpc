package encoder

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"io"
	"net"

	"github.com/antontroskie/radrpc/pkg/rpc/itf"
	"github.com/google/uuid"
	"github.com/pkg/errors"
)

// EncodeMessage encodes a message.
func EncodeMessage(message any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := gob.NewEncoder(&buffer)
	if err := encoder.Encode(message); err != nil {
		return nil, errors.Wrap(err, "error encoding")
	}
	return buffer.Bytes(), nil
}

// DecodeMessage decodes a message.
func DecodeMessage[T any](data []byte) (T, error) {
	var message T
	if len(data) == 0 {
		return message, nil
	}
	buffer := bytes.NewBuffer(data)
	decoder := gob.NewDecoder(buffer)
	if err := decoder.Decode(&message); err != nil {
		return message, errors.Wrap(err, "error decoding")
	}
	return message, nil
}

// WriteHeartbeatRequest writes a heartbeat request.
func WriteHeartbeatRequest(conn net.Conn) error {
	id := uuid.New().String()
	msg := itf.RPCMessageReq{
		ID:        id,
		Type:      itf.HeartbeatRequest,
		TimeStamp: 0,
		Method:    "",
		Args:      []any{},
	}
	data, err := EncodeMessage(msg)
	if err != nil {
		return err
	}
	return WriteMessage(conn, data)
}

// WriteHeartbeatResponse writes a heartbeat response.
func WriteHeartbeatResponse(conn net.Conn, id string) error {
	msg := itf.RPCMessageRes{
		ID:              id,
		Type:            itf.HeartbeatResponse,
		TimeStamp:       0,
		ResponseError:   "",
		ResponseSuccess: nil,
	}
	data, err := EncodeMessage(msg)
	if err != nil {
		return err
	}
	return WriteMessage(conn, data)
}

// WriteMessage writes a message.
func WriteMessage(conn net.Conn, msg []byte) error {
	length := uint64(len(msg))
	if err := binary.Write(conn, binary.LittleEndian, length); err != nil {
		return errors.Wrap(err, "error writing binary")
	}
	if len(msg) == 0 {
		return nil
	}
	if _, err := conn.Write(msg); err != nil {
		return errors.Wrap(err, "error writing message")
	}
	return nil
}

// ReadMessage reads a message.
func ReadMessage(conn net.Conn) ([]byte, error) {
	var length uint64
	if err := binary.Read(conn, binary.LittleEndian, &length); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.Wrap(err, "error reading binary")
		}
	}
	if length == 0 {
		return nil, nil
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.Wrap(err, "error reading message")
		}
	}
	return data, nil
}
