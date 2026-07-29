package biographyvoice

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// Doubao uses the same four-byte binary envelope across streaming ASR and
// bidirectional TTS. The layout follows the public VolcEngine protocol docs.
const (
	doubaoMessageFullClient      byte = 0x1
	doubaoMessageAudioClient     byte = 0x2
	doubaoMessageFullServer      byte = 0x9
	doubaoMessageAudioServer     byte = 0xB
	doubaoMessageError           byte = 0xF
	doubaoFlagNone               byte = 0x0
	doubaoFlagPositiveSequence   byte = 0x1
	doubaoFlagLastNoSequence     byte = 0x2
	doubaoFlagLastWithSequence   byte = 0x3
	doubaoFlagWithEvent          byte = 0x4
	doubaoSerializationNone      byte = 0x0
	doubaoSerializationJSON      byte = 0x1
	doubaoCompressionNone        byte = 0x0
	doubaoCompressionGzip        byte = 0x1
	doubaoMaxDecodedPayloadBytes      = 10 * 1024 * 1024
)

const (
	doubaoEventStartConnection    int32 = 1
	doubaoEventFinishConnection   int32 = 2
	doubaoEventConnectionStarted  int32 = 50
	doubaoEventConnectionFailed   int32 = 51
	doubaoEventConnectionFinished int32 = 52
	doubaoEventStartSession       int32 = 100
	doubaoEventCancelSession      int32 = 101
	doubaoEventFinishSession      int32 = 102
	doubaoEventTaskRequest        int32 = 200
	doubaoEventSessionStarted     int32 = 150
	doubaoEventSessionCanceled    int32 = 151
	doubaoEventSessionFinished    int32 = 152
	doubaoEventSessionFailed      int32 = 153
	doubaoEventTTSResponse        int32 = 352
)

type doubaoFrame struct {
	MessageType   byte
	Flags         byte
	Serialization byte
	Compression   byte
	Sequence      int32
	HasSequence   bool
	Event         int32
	HasEvent      bool
	EventID       string
	ErrorCode     uint32
	Payload       []byte
}

func buildDoubaoASRStart(payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{
		MessageType: doubaoMessageFullClient, Serialization: doubaoSerializationJSON,
		Compression: doubaoCompressionNone, Payload: payload,
	})
}

func buildDoubaoASRAudio(payload []byte, last bool) ([]byte, error) {
	flags := doubaoFlagNone
	if last {
		flags = doubaoFlagLastNoSequence
	}
	return buildDoubaoFrame(doubaoFrame{
		MessageType: doubaoMessageAudioClient, Flags: flags,
		Serialization: doubaoSerializationNone, Compression: doubaoCompressionNone, Payload: payload,
	})
}

func buildDoubaoTTSConnectEvent(event int32, payload []byte) ([]byte, error) {
	return buildDoubaoFrame(doubaoFrame{
		MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent,
		Serialization: doubaoSerializationJSON, Event: event, HasEvent: true, Payload: payload,
	})
}

func buildDoubaoTTSSessionEvent(sessionID string, event int32, payload []byte) ([]byte, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("doubao TTS session id is required")
	}
	return buildDoubaoFrame(doubaoFrame{
		MessageType: doubaoMessageFullClient, Flags: doubaoFlagWithEvent,
		Serialization: doubaoSerializationJSON, Event: event, HasEvent: true,
		EventID: sessionID, Payload: payload,
	})
}

func buildDoubaoFrame(frame doubaoFrame) ([]byte, error) {
	flags := frame.Flags
	if frame.HasEvent {
		flags |= doubaoFlagWithEvent
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 24+len(frame.EventID)+len(frame.Payload)))
	buffer.WriteByte(0x11)
	buffer.WriteByte(frame.MessageType<<4 | flags)
	buffer.WriteByte(frame.Serialization<<4 | frame.Compression)
	buffer.WriteByte(0)

	if frame.HasSequence {
		if err := binary.Write(buffer, binary.BigEndian, frame.Sequence); err != nil {
			return nil, fmt.Errorf("write doubao sequence: %w", err)
		}
	}
	if frame.HasEvent {
		if err := binary.Write(buffer, binary.BigEndian, frame.Event); err != nil {
			return nil, fmt.Errorf("write doubao event: %w", err)
		}
		if shouldCarryDoubaoEventID(frame.Event) {
			if err := binary.Write(buffer, binary.BigEndian, uint32(len(frame.EventID))); err != nil {
				return nil, fmt.Errorf("write doubao event id length: %w", err)
			}
			if _, err := buffer.WriteString(frame.EventID); err != nil {
				return nil, fmt.Errorf("write doubao event id: %w", err)
			}
		}
	}
	if frame.MessageType == doubaoMessageError {
		if err := binary.Write(buffer, binary.BigEndian, frame.ErrorCode); err != nil {
			return nil, fmt.Errorf("write doubao error code: %w", err)
		}
	}

	payload := frame.Payload
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		compressed, err := gzipDoubaoPayload(payload)
		if err != nil {
			return nil, err
		}
		payload = compressed
	}
	if err := binary.Write(buffer, binary.BigEndian, uint32(len(payload))); err != nil {
		return nil, fmt.Errorf("write doubao payload length: %w", err)
	}
	if _, err := buffer.Write(payload); err != nil {
		return nil, fmt.Errorf("write doubao payload: %w", err)
	}
	return buffer.Bytes(), nil
}

func parseDoubaoFrame(data []byte) (doubaoFrame, error) {
	if len(data) < 8 {
		return doubaoFrame{}, fmt.Errorf("doubao frame too short: %d", len(data))
	}
	headerBytes := int(data[0]&0x0F) * 4
	if headerBytes < 4 || len(data) < headerBytes {
		return doubaoFrame{}, fmt.Errorf("invalid doubao header size: %d", headerBytes)
	}
	frame := doubaoFrame{
		MessageType: (data[1] >> 4) & 0x0F,
		Flags:       data[1] & 0x0F, Serialization: (data[2] >> 4) & 0x0F, Compression: data[2] & 0x0F,
	}
	offset := headerBytes
	sequenceFlags := frame.Flags & 0x03
	if sequenceFlags == doubaoFlagPositiveSequence || sequenceFlags == doubaoFlagLastWithSequence {
		if len(data) < offset+4 {
			return doubaoFrame{}, fmt.Errorf("doubao frame missing sequence")
		}
		frame.HasSequence = true
		frame.Sequence = int32(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
	}
	if frame.Flags&doubaoFlagWithEvent != 0 {
		if len(data) < offset+4 {
			return doubaoFrame{}, fmt.Errorf("doubao frame missing event")
		}
		frame.HasEvent = true
		frame.Event = int32(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if shouldCarryDoubaoEventID(frame.Event) {
			if len(data) < offset+4 {
				return doubaoFrame{}, fmt.Errorf("doubao frame missing event id length")
			}
			idLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			offset += 4
			if idLength < 0 || len(data) < offset+idLength {
				return doubaoFrame{}, fmt.Errorf("invalid doubao event id length: %d", idLength)
			}
			frame.EventID = string(data[offset : offset+idLength])
			offset += idLength
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
		return doubaoFrame{}, fmt.Errorf("doubao frame missing payload length")
	}
	payloadLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if payloadLength < 0 || len(data) < offset+payloadLength {
		return doubaoFrame{}, fmt.Errorf("invalid doubao payload length: %d", payloadLength)
	}
	payload := data[offset : offset+payloadLength]
	if frame.Compression == doubaoCompressionGzip && len(payload) > 0 {
		decoded, err := gunzipDoubaoPayload(payload)
		if err != nil {
			return doubaoFrame{}, err
		}
		payload = decoded
	}
	frame.Payload = append([]byte(nil), payload...)
	return frame, nil
}

func shouldCarryDoubaoEventID(event int32) bool {
	return event == doubaoEventConnectionStarted || event == doubaoEventConnectionFailed ||
		event == doubaoEventConnectionFinished || event >= doubaoEventStartSession
}

func gzipDoubaoPayload(payload []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		return nil, fmt.Errorf("gzip doubao payload: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close doubao gzip writer: %w", err)
	}
	return buffer.Bytes(), nil
}

func gunzipDoubaoPayload(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open doubao gzip payload: %w", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, doubaoMaxDecodedPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read doubao gzip payload: %w", err)
	}
	if len(decoded) > doubaoMaxDecodedPayloadBytes {
		return nil, fmt.Errorf("doubao gzip payload exceeds %d bytes", doubaoMaxDecodedPayloadBytes)
	}
	return decoded, nil
}
