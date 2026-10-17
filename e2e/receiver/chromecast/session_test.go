package chromecast

import (
	"encoding/binary"
	"encoding/json/v2"
	"io"
	"net"
	"testing"
	"time"

	pb "github.com/vishen/go-chromecast/cast/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

func TestReceiverAnswersHeartbeatOnTheWire(t *testing.T) {
	sender, device := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { sender.Close(); device.Close(); <-done })
	go func() {
		defer close(done)
		(&castSession{conn: device}).serve()
	}()
	if err := sender.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	from, to := "sender-0", "receiver-0"
	namespace, payload := "urn:x-cast:com.google.cast.tp.heartbeat", `{"type":"PING"}`
	frame, err := proto.Marshal(protoadapt.MessageV2Of(&pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(), SourceId: &from,
		DestinationId: &to, Namespace: &namespace,
		PayloadType: pb.CastMessage_STRING.Enum(), PayloadUtf8: &payload,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(frame))), frame...)); err != nil {
		t.Fatal(err)
	}
	var size uint32
	if err := binary.Read(sender, binary.BigEndian, &size); err != nil {
		t.Fatalf("receiver did not answer heartbeat: %v", err)
	}
	answer := make([]byte, size)
	if _, err := io.ReadFull(sender, answer); err != nil {
		t.Fatal(err)
	}
	var message pb.CastMessage
	if err := proto.Unmarshal(answer, protoadapt.MessageV2Of(&message)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(message.GetPayloadUtf8()), &body); err != nil {
		t.Fatal(err)
	}
	if body.Type != "PONG" || message.GetNamespace() != namespace || message.GetDestinationId() != from {
		t.Fatalf("wrong heartbeat response: %s", &message)
	}
}
