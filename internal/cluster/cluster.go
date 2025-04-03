package cluster

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/sonroyaalmerol/phpcloud/internal/config"
	"go.uber.org/zap"
)

// Manager handles cluster coordination using memberlist
type Manager struct {
	config     *config.Config
	logger     *zap.Logger
	nodeName   string
	memberlist *memberlist.Memberlist
	events     chan memberlist.NodeEvent
	isLeader   bool
}

// New creates a new cluster manager
func New(cfg *config.Config, nodeName string, logger *zap.Logger) (*Manager, error) {
	return &Manager{
		config:   cfg,
		logger:   logger,
		nodeName: nodeName,
		events:   make(chan memberlist.NodeEvent, 100),
	}, nil
}

// Start initializes and joins the cluster
func (m *Manager) Start() error {
	m.logger.Info("Starting cluster coordination",
		zap.String("node_name", m.nodeName),
		zap.String("discovery", m.config.Cluster.Discovery),
	)

	// Configure memberlist
	conf := memberlist.DefaultLANConfig()
	conf.Name = m.nodeName
	conf.BindPort = m.config.Server.GossipPort
	conf.AdvertisePort = m.config.Server.GossipPort
	conf.LogOutput = os.Stderr

	// Create memberlist
	list, err := memberlist.Create(conf)
	if err != nil {
		return fmt.Errorf("failed to create memberlist: %w", err)
	}

	m.memberlist = list

	// Discover and join peers
	if err := m.joinPeers(); err != nil {
		m.logger.Warn("Failed to join peers", zap.Error(err))
		// Continue anyway - single node mode
	}

	m.logger.Info("Cluster coordination started",
		zap.Int("members", m.memberlist.NumMembers()),
	)

	return nil
}

// Leave gracefully leaves the cluster
func (m *Manager) Leave() error {
	if m.memberlist != nil {
		return m.memberlist.Leave(5 * time.Second)
	}
	return nil
}

// IsLeader returns true if this node is the cluster leader
func (m *Manager) IsLeader() bool {
	if m.memberlist == nil {
		return true // Single node mode
	}

	members := m.memberlist.Members()
	if len(members) == 0 {
		return true
	}

	// Sort members by name lexicographically
	sort.Slice(members, func(i, j int) bool {
		return members[i].Name < members[j].Name
	})

	return members[0].Name == m.nodeName
}

// GetMembers returns the list of cluster members
func (m *Manager) GetMembers() []string {
	if m.memberlist == nil {
		return []string{m.nodeName}
	}

	members := m.memberlist.Members()
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	return names
}

// Broadcast sends a message to all cluster members
func (m *Manager) Broadcast(msg []byte) {
	// Broadcast functionality would use memberlist's SendReliable or similar
	// This is a placeholder for future implementation
	m.logger.Debug("Broadcast message", zap.Int("size", len(msg)))
}

// joinPeers discovers and joins cluster peers
func (m *Manager) joinPeers() error {
	var peers []string

	switch m.config.Cluster.Discovery {
	case "dns":
		peers = m.discoverDNS()
	case "static":
		peers = m.config.Cluster.StaticPeers
	case "env":
		// TODO: Read from environment variable
	}

	if len(peers) == 0 {
		m.logger.Info("No peers discovered, operating in single node mode")
		return nil
	}

	m.logger.Info("Joining peers", zap.Strings("peers", peers))

	_, err := m.memberlist.Join(peers)
	if err != nil {
		return fmt.Errorf("failed to join cluster: %w", err)
	}

	return nil
}

// discoverDNS discovers peers via DNS
func (m *Manager) discoverDNS() []string {
	// This would typically resolve a headless k8s service
	// For now, return empty - will be implemented with proper DNS resolution
	m.logger.Info("DNS discovery not yet implemented")
	return nil
}

// LocalNode returns the local node info
func (m *Manager) LocalNode() *memberlist.Node {
	if m.memberlist != nil {
		return m.memberlist.LocalNode()
	}
	return nil
}
