// Package router handles message routing to appropriate agents.
package router

import (
	"time"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/routing"
)

// Router handles routing of messages to the appropriate agents.
type Router struct {
	agentRegistry AgentRegistry
}

// AgentRegistry defines the interface for agent registry operations.
type AgentRegistry interface {
	ResolveRoute(input routing.RouteInput) routing.ResolvedRoute
	GetAgent(agentID string) (*interfaces.AgentInstance, bool)
	GetDefaultAgent() *interfaces.AgentInstance
	ListAgentIDs() []string
}

// NewRouter creates a new router instance.
func NewRouter(agentRegistry AgentRegistry) *Router {
	return &Router{
		agentRegistry: agentRegistry,
	}
}

// RouteMessage determines which agent should handle a given message.
func (r *Router) RouteMessage(msg bus.InboundMessage) (*RoutingDecision, error) {
	// Route to determine agent and session key
	route := r.agentRegistry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := r.agentRegistry.GetAgent(route.AgentID)
	if !ok {
		agent = r.agentRegistry.GetDefaultAgent()
		if agent == nil {
			return nil, nil
		}
	}

	// Use routed session key, but honor pre-set agent-scoped keys (for ProcessDirect/cron)
	sessionKey := route.SessionKey
	if msg.SessionKey != "" && len(msg.SessionKey) > 6 && msg.SessionKey[:6] == "agent:" {
		sessionKey = msg.SessionKey
	}

	return &RoutingDecision{
		OriginalMessage: msg,
		AgentID:         agent.ID,
		SessionKey:      sessionKey,
		RouteType:       route.MatchedBy,
		Timestamp:       time.Now(),
	}, nil
}

// RoutingDecision represents the decision of which agent should handle a message.
type RoutingDecision struct {
	OriginalMessage bus.InboundMessage
	AgentID         string
	SessionKey      string
	RouteType       string // direct, default, etc.
	Timestamp       time.Time
}

// extractPeer extracts the routing peer from inbound message metadata.
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	peerKind := msg.Metadata["peer_kind"]
	if peerKind == "" {
		return nil
	}
	peerID := msg.Metadata["peer_id"]
	if peerID == "" {
		if peerKind == "direct" {
			peerID = msg.SenderID
		} else {
			peerID = msg.ChatID
		}
	}
	return &routing.RoutePeer{Kind: peerKind, ID: peerID}
}

// extractParentPeer extracts the parent peer (reply-to) from inbound message metadata.
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	parentKind := msg.Metadata["parent_peer_kind"]
	parentID := msg.Metadata["parent_peer_id"]
	if parentKind == "" || parentID == "" {
		return nil
	}
	return &routing.RoutePeer{Kind: parentKind, ID: parentID}
}