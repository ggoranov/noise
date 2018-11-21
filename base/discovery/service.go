package discovery

import (
	"github.com/gogo/protobuf/proto"
	"github.com/perlin-network/noise/dht"
	"github.com/perlin-network/noise/internal/protobuf"
	"github.com/perlin-network/noise/log"
	"github.com/perlin-network/noise/peer"
	"github.com/perlin-network/noise/protocol"
	"github.com/pkg/errors"
)

// Service is a service that handles periodic lookups of remote peers
type Service struct {
	DisablePing   bool
	DisablePong   bool
	DisableLookup bool

	Routes      *dht.RoutingTable
	sendHandler SendHandler
}

// NewService creates a new instance of the Discovery Service
func NewService(sendHandler SendHandler, selfID peer.ID) *Service {
	return &Service{
		Routes:      dht.CreateRoutingTable(selfID),
		sendHandler: sendHandler,
	}
}

// ReceiveHandler is the handler when a message is received
func (s *Service) ReceiveHandler(message *protocol.Message) (*protocol.MessageBody, error) {

	if message == nil || message.Body == nil || message.Body.Service != DiscoveryServiceID {
		// corrupt message so ignore
		return nil, errors.New("Message is corrupt")
	}
	if len(message.Body.Payload) == 0 {
		// corrupt payload so ignore
		return nil, errors.New("Message body is missing")
	}

	sender, ok := s.Routes.LookupRemoteAddress(message.Sender)
	if !ok {
		return nil, errors.New("Unable to lookup sender")
	}
	target, ok := s.Routes.LookupRemoteAddress(message.Recipient)
	if !ok {
		// TODO: handle known peer
		return nil, errors.New("Unable to lookup recipient")
	}

	var msg protobuf.Message
	if err := proto.Unmarshal(message.Body.Payload, &msg); err != nil {
		// unknown type so ignore
		return nil, errors.Wrap(err, "Unable to parse message")
	}

	reply, err := s.receive(*sender, *target, msg)
	if err != nil {
		return nil, err
	}

	return reply, nil
}

func (s *Service) receive(sender peer.ID, target peer.ID, msg protobuf.Message) (*protocol.MessageBody, error) {
	// update the routes on every message
	s.Routes.Update(sender)

	switch msg.Opcode {
	case opCodePing:
		if s.DisablePing {
			break
		}
		// send the pong to the peer
		return makeMessageBody(opCodePong, &protobuf.Pong{})
	case opCodePong:
		if s.DisablePong {
			break
		}
		peers := FindNode(s.Routes, s.sendHandler, sender, dht.BucketSize, 8)

		// Update routing table w/ closest peers to self.
		for _, peerID := range peers {
			s.Routes.Update(peerID)
		}

		log.Info().
			Strs("peers", s.Routes.GetPeerAddresses()).
			Msg("Bootstrapped w/ peer(s).")
	case opCodeLookupRequest:
		if s.DisableLookup {
			break
		}

		// Prepare response
		response := &protobuf.LookupNodeResponse{}

		// Respond back with closest peers to a provided target.
		for _, peerID := range s.Routes.FindClosestPeers(target, dht.BucketSize) {
			id := protobuf.ID(peerID)
			response.Peers = append(response.Peers, &id)
		}

		return makeMessageBody(opCodeLookupResponse, response)
	default:
		return nil, errors.Errorf("Unknown message opcode type: %d", msg.Opcode)
	}
	return nil, nil
}

// PeerDisconnect handles updating the routing table on disconnect
func (s *Service) PeerDisconnect(target peer.ID) {
	// Delete peer if in routing table.
	if s.Routes.PeerExists(target) {
		s.Routes.RemovePeer(target)

		log.Debug().
			Str("address", s.Routes.Self().Address).
			Str("peer_address", target.Address).
			Msg("Peer has disconnected.")
	}
}

func makeMessageBody(opcode int, content proto.Message) (*protocol.MessageBody, error) {
	msg, err := toProtobufMessage(opcode, content)
	if err != nil {
		return nil, err
	}
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to marshal")
	}
	return &protocol.MessageBody{
		Service: DiscoveryServiceID,
		Payload: msgBytes,
	}, nil
}
