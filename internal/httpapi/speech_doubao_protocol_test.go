package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	doubaoMessageFullClient    byte = 0x1
	doubaoMessageAudioClient   byte = 0x2
	doubaoMessageFullServer    byte = 0x9
	doubaoMessageAudioServer   byte = 0xB
	doubaoMessageError         byte = 0xF
	doubaoFlagNone             byte = 0x0
	doubaoFlagPositiveSequence byte = 0x1
	doubaoFlagLastNoSequence   byte = 0x2
	doubaoFlagLastWithSequence byte = 0x3
	doubaoFlagWithEvent        byte = 0x4
	doubaoSerializationNone    byte = 0x0
	doubaoSerializationJSON    byte = 0x1
	doubaoCompressionNone      byte = 0x0
	doubaoCompressionGzip      byte = 0x1
	doubaoMaxDecodedPayload         = 10 * 1024 * 1024
)

const (
	doubaoEventStartConnection   int32 = 1
	doubaoEventFinishConnection  int32 = 2
	doubaoEventConnectionStarted int32 = 50
	doubaoEventConnectionFailed  int32 = 51
	doubaoEventStartSession      int32 = 100
	doubaoEventCancelSession     int32 = 101
	doubaoEventFinishSession     int32 = 102
	doubaoEventTaskRequest       int32 = 200
	doubaoEventSessionStarted    int32 = 150
	doubaoEventSessionCanceled   int32 = 151
	doubaoEventSessionFinished   int32 = 152
	doubaoEventSessionFailed     int32 = 153
	doubaoEventTTSResponse       int32 = 352
)

type doubaoFrame struct {
	MessageType, Flags, Serialization, Compression byte
	Sequence                                       int32
	HasSequence                                    bool
	Event                                          int32
	HasEvent                                       bool
	EventID                                        string
	ErrorCode                                      uint32
	Payload                                        []byte
}

func buildDoubaoASRStart(payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Serialization: doubaoSerializationJSON, Payload: payload})
}

func buildDoubaoASRAudio(payload []byte, last bool) ([]byte, error) {
	flags := doubaoFlagNone
	if last {
		flags = doubaoFlagLastNoSequence
	}
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageAudioClient, Flags: flags, Serialization: doubaoSerializationNone, Payload: payload})
}

func buildDoubaoTTSConnectEvent(event int32, payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent, Serialization: doubaoSerializationJSON, Event: event, HasEvent: true, Payload: payload})
}

func buildDoubaoTTSSessionEvent(sessionID string, event int32, payload []byte) ([]byte, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("doubao TTS session id is required")
	}
	return buildDoubaoFrame(doubaoFrame{MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent, Serialization: doubaoSerializationJSON, Event: event, HasEvent: true, EventID: sessionID, Payload: payload})
}

func buildDoubaoFrame(frame doubaoFrame) ([]byte, error) {
	flags := frame.Flags
	if frame.HasEvent {
		flags |= doubaoFlagWithEvent
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 24+len(frame.EventID)+len(frame.Payload)))
	buffer.Write([]byte{0x11, frame.MessageType<<4 | flags, frame.Serialization<<4 | frame.Compression, 0})
	if frame.HasSequence {
		_ = binary.Write(buffer, binary.BigEndian, frame.Sequence)
	}
	if frame.HasEvent {
		_ = binary.Write(buffer, binary.BigEndian, frame.Event)
		if doubaoEventCarriesID(frame.Event) {
			_ = binary.Write(buffer, binary.BigEndian, uint32(len(frame.EventID)))
			buffer.WriteString(frame.EventID)
		}
	}
	if frame.MessageType == doubaoMessageError {
		_ = binary.Write(buffer, binary.BigEndian, frame.ErrorCode)
	}
	payload := frame.Payload
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(payload); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		payload = compressed.Bytes()
	}
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(payload)))
	buffer.Write(payload)
	return buffer.Bytes(), nil
}

func parseDoubaoFrame(data []byte) (doubaoFrame, error) {
	if len(data) < 8 {
		return doubaoFrame{}, fmt.Errorf("doubao frame too short")
	}
	headerBytes := int(data[0]&0x0F) * 4
	if headerBytes < 4 || len(data) < headerBytes {
		return doubaoFrame{}, fmt.Errorf("invalid doubao header")
	}
	frame := doubaoFrame{MessageType: data[1] >> 4, Flags: data[1] & 0x0F, Serialization: data[2] >> 4, Compression: data[2] & 0x0F}
	offset := headerBytes
	sequenceFlags := frame.Flags & 0x03
	if sequenceFlags == doubaoFlagPositiveSequence || sequenceFlags == doubaoFlagLastWithSequence {
		if len(data) < offset+4 {
			return doubaoFrame{}, fmt.Errorf("doubao frame missing sequence")
		}
		frame.HasSequence, frame.Sequence = true, int32(binary.BigEndian.Uint32(data[offset:offset+4]))
		offset += 4
	}
	if frame.Flags&doubaoFlagWithEvent != 0 {
		if len(data) < offset+4 {
			return doubaoFrame{}, fmt.Errorf("doubao frame missing event")
		}
		frame.HasEvent, frame.Event = true, int32(binary.BigEndian.Uint32(data[offset:offset+4]))
		offset += 4
		if doubaoEventCarriesID(frame.Event) {
			if len(data) < offset+4 {
				return doubaoFrame{}, fmt.Errorf("doubao frame missing event id")
			}
			length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
			if length < 0 || len(data) < offset+length {
				return doubaoFrame{}, fmt.Errorf("invalid doubao event id")
			}
			frame.EventID = string(data[offset : offset+length])
			offset += length
		}
	}
	if frame.MessageType == doubaoMessageError {
		if len(data) < offset+4 {
			return doubaoFrame{}, fmt.Errorf("doubao frame missing error code")
		}
		frame.ErrorCode = binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
	}
	if len(data) < offset+4 {
		return doubaoFrame{}, fmt.Errorf("doubao frame missing payload")
	}
	length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if length < 0 || len(data) < offset+length {
		return doubaoFrame{}, fmt.Errorf("invalid doubao payload length")
	}
	payload := data[offset : offset+length]
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return doubaoFrame{}, err
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, doubaoMaxDecodedPayload+1))
		_ = reader.Close()
		if err != nil || len(decoded) > doubaoMaxDecodedPayload {
			return doubaoFrame{}, fmt.Errorf("decode doubao compressed payload")
		}
		payload = decoded
	}
	frame.Payload = append([]byte(nil), payload...)
	return frame, nil
}

func doubaoEventCarriesID(event int32) bool {
	return event == doubaoEventConnectionStarted || event == doubaoEventConnectionFailed || event >= doubaoEventStartSession
}
