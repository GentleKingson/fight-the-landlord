package server

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/palemoky/fight-the-landlord/internal/protocol"
	"github.com/palemoky/fight-the-landlord/internal/protocol/codec"
	"github.com/palemoky/fight-the-landlord/internal/update"
)

const (
	handshakeWait          = 10 * time.Second
	maxClientVersionLength = 64
	maxCapabilities        = 16
	maxCapabilityLength    = 64
	maxRequestIDLength     = 128
)

var releaseVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type negotiatedClient struct {
	version      string
	capabilities []string
	kind         string
}

func (s *Server) negotiateWebSocket(conn *websocket.Conn) (negotiatedClient, error) {
	if conn == nil {
		return negotiatedClient{}, fmt.Errorf("nil websocket connection")
	}
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(handshakeWait))

	frameType, frame, err := conn.ReadMessage()
	if err != nil {
		return negotiatedClient{}, fmt.Errorf("read protocol hello: %w", err)
	}
	if frameType != websocket.BinaryMessage {
		s.rejectProtocol(conn, "握手必须使用二进制 protobuf 帧", "")
		return negotiatedClient{}, fmt.Errorf("protocol hello used websocket frame type %d", frameType)
	}

	msg, err := codec.Decode(frame)
	if err != nil {
		s.rejectProtocol(conn, "无法解析协议握手", "")
		return negotiatedClient{}, fmt.Errorf("decode protocol hello: %w", err)
	}
	defer codec.PutMessage(msg)

	requestID := ""
	if msg.Command != nil {
		requestID = msg.Command.RequestID
	}
	if msg.Type != protocol.MsgHello {
		s.rejectProtocol(conn, "连接后的第一条消息必须是 hello", requestID)
		return negotiatedClient{}, fmt.Errorf("first message was %q, want hello", msg.Type)
	}
	if !validRequestID(requestID) {
		s.rejectProtocol(conn, "hello 缺少有效的 request_id", "")
		return negotiatedClient{}, fmt.Errorf("invalid hello request id")
	}

	hello, err := codec.ParsePayload[protocol.HelloPayload](msg)
	if err != nil {
		s.rejectProtocol(conn, "hello 内容无效", requestID)
		return negotiatedClient{}, fmt.Errorf("decode hello payload: %w", err)
	}
	if reason := s.validateHello(*hello); reason != "" {
		s.rejectProtocol(conn, reason, requestID)
		return negotiatedClient{}, fmt.Errorf("protocol rejected: %s", reason)
	}

	negotiated := negotiatedClient{
		version:      strings.TrimSpace(hello.ClientVersion),
		capabilities: append([]string(nil), protocol.RequiredCapabilities...),
		kind:         hello.ClientKind,
	}
	response := codec.MustNewMessage(protocol.MsgNegotiated, protocol.NegotiatedPayload{
		ProtocolVersion: protocol.ProtocolVersion,
		ServerVersion:   Version,
		Capabilities:    append([]string(nil), negotiated.capabilities...),
		ClientKind:      negotiated.kind,
	})
	response.Command = &protocol.CommandMeta{RequestID: requestID}
	if err := writeWebSocketProtocolMessage(conn, response); err != nil {
		return negotiatedClient{}, fmt.Errorf("write negotiated response: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	return negotiated, nil
}

func (s *Server) validateHello(hello protocol.HelloPayload) string {
	if hello.ProtocolVersion != protocol.ProtocolVersion {
		return fmt.Sprintf("协议版本不兼容：需要 %s", protocol.ProtocolVersion)
	}
	clientVersion := strings.TrimSpace(hello.ClientVersion)
	if clientVersion == "" || len(clientVersion) > maxClientVersionLength {
		return "client_version 无效"
	}
	if !validClientKind(hello.ClientKind) {
		return "client_kind 无效"
	}
	if reason := validateCapabilities(hello.Capabilities); reason != "" {
		return reason
	}
	minimum := ""
	if s != nil && s.config != nil {
		minimum = strings.TrimSpace(s.config.Server.MinClientVersion)
	}
	if minimum != "" && !compatibleClientVersion(clientVersion, minimum, Version) {
		return fmt.Sprintf("客户端版本过低：需要至少 %s", minimum)
	}
	return ""
}

func validClientKind(kind string) bool {
	return kind == protocol.ClientKindWeb || kind == protocol.ClientKindTUI || kind == protocol.ClientKindBot
}

func validateCapabilities(values []string) string {
	if len(values) > maxCapabilities {
		return "capabilities 数量超过限制"
	}
	capabilities := make(map[string]struct{}, len(values))
	for _, capability := range values {
		if capability == "" || len(capability) > maxCapabilityLength {
			return "capability 无效"
		}
		if _, duplicate := capabilities[capability]; duplicate {
			return "capabilities 包含重复项"
		}
		capabilities[capability] = struct{}{}
	}
	for _, required := range protocol.RequiredCapabilities {
		if _, ok := capabilities[required]; !ok {
			return fmt.Sprintf("缺少必需 capability：%s", required)
		}
	}
	return ""
}

func compatibleClientVersion(clientVersion, minimumVersion, serverVersion string) bool {
	if releaseVersionPattern.MatchString(clientVersion) && releaseVersionPattern.MatchString(minimumVersion) {
		return update.CompareVersions(clientVersion, minimumVersion) >= 0
	}
	// Local and CI builds are allowed only when the server itself is also a
	// non-release build. A release server never accepts an opaque version.
	return !releaseVersionPattern.MatchString(serverVersion) && slices.Contains([]string{"dev", "ci"}, strings.ToLower(clientVersion))
}

func (s *Server) rejectProtocol(conn *websocket.Conn, reason, requestID string) {
	minimum := ""
	if s != nil && s.config != nil {
		minimum = s.config.Server.MinClientVersion
	}
	msg := codec.MustNewMessage(protocol.MsgProtocolRejected, protocol.ProtocolRejectedPayload{
		RequestID:                requestID,
		Reason:                   reason,
		SupportedProtocolVersion: protocol.ProtocolVersion,
		MinClientVersion:         minimum,
	})
	if requestID != "" {
		msg.Command = &protocol.CommandMeta{RequestID: requestID}
	}
	_ = writeWebSocketProtocolMessage(conn, msg)
}

func writeWebSocketProtocolMessage(conn *websocket.Conn, msg *protocol.Message) error {
	data, err := codec.Encode(msg)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func validRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > maxRequestIDLength {
		return false
	}
	for _, char := range requestID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '-', '_', '.', ':':
			continue
		default:
			return false
		}
	}
	return true
}
