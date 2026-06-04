// Package tree implements a hierarchical tree-like memory system for QuantClaw.
// Based on the concept of Memory-Like-A-Tree architecture where memory is organized
// in a tree structure with nodes representing different levels of abstraction and context.
package tree

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// NodeType represents different types of memory nodes
type NodeType string

const (
	NodeTypeRoot      NodeType = "root"
	NodeTypeTopic     NodeType = "topic"
	NodeTypeSession   NodeType = "session"
	NodeTypeEvent     NodeType = "event"
	NodeTypeFact      NodeType = "fact"
	NodeTypeSummary   NodeType = "summary"
	NodeTypeTemporal  NodeType = "temporal" // 时间节点，如年/月/日
)

// MemoryNode represents a node in the hierarchical memory tree
type MemoryNode struct {
	ID          string                 `json:"id"`
	Type        NodeType               `json:"type"`
	Title       string                 `json:"title"`
	Content     string                 `json:"content"`
	Tags        []string               `json:"tags,omitempty"`
	Embedding   []float32              `json:"embedding,omitempty"` // For semantic similarity
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	ParentID    string                 `json:"parent_id,omitempty"`
	Children    []string               `json:"children,omitempty"` // Child node IDs
	Metadata    map[string]interface{} `json:"metadata,omitempty"` // Additional metadata
	VectorIndex int                    `json:"vector_index,omitempty"` // Index in vector store
}

// NodeManager manages the hierarchical memory tree
type NodeManager struct {
	nodes      map[string]*MemoryNode
	nodesMutex sync.RWMutex

	// Storage paths
	workspace string
	nodesDir  string

	// Cache for frequently accessed nodes
	cache     map[string]*MemoryNode
	cacheSize int
}

// NewNodeManager creates a new hierarchical memory manager
func NewNodeManager(workspace string) *NodeManager {
	nodesDir := filepath.Join(workspace, "memory", "nodes")

	return &NodeManager{
		nodes:     make(map[string]*MemoryNode),
		workspace: workspace,
		nodesDir:  nodesDir,
		cache:     make(map[string]*MemoryNode),
		cacheSize: 100, // Default cache size
	}
}

// CreateNode creates a new memory node in the tree
func (nm *NodeManager) CreateNode(nodeType NodeType, title, content string, parentID string, tags []string) (*MemoryNode, error) {
	nm.nodesMutex.Lock()
	defer nm.nodesMutex.Unlock()

	nodeID := fmt.Sprintf("%s_%d", strings.ToLower(string(nodeType)), time.Now().UnixNano())

	node := &MemoryNode{
		ID:        nodeID,
		Type:      nodeType,
		Title:     title,
		Content:   content,
		Tags:      tags,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ParentID:  parentID,
		Children:  []string{},
		Metadata:  make(map[string]interface{}),
	}

	nm.nodes[nodeID] = node

	// Add to parent's children list
	if parentID != "" {
		if parentNode, exists := nm.nodes[parentID]; exists {
			parentNode.Children = append(parentNode.Children, nodeID)
			parentNode.UpdatedAt = time.Now()
		}
	}

	// Update cache
	nm.updateCache(nodeID, node)

	return node, nil
}

// GetNode retrieves a memory node by ID
func (nm *NodeManager) GetNode(id string) (*MemoryNode, bool) {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	// Check cache first
	if cached, exists := nm.cache[id]; exists {
		return cached, true
	}

	node, exists := nm.nodes[id]
	if !exists {
		return nil, false
	}

	// Update cache
	nm.updateCache(id, node)

	return node, true
}

// updateCache updates the cache with the given node
func (nm *NodeManager) updateCache(id string, node *MemoryNode) {
	// Simple LRU-like cache management
	if len(nm.cache) >= nm.cacheSize {
		// Remove oldest entry (in a real implementation, you'd use a proper LRU)
		for cacheID := range nm.cache {
			delete(nm.cache, cacheID)
			break
		}
	}

	nm.cache[id] = node
}

// UpdateNode updates an existing memory node
func (nm *NodeManager) UpdateNode(id, title, content string, tags []string) error {
	nm.nodesMutex.Lock()
	defer nm.nodesMutex.Unlock()

	node, exists := nm.nodes[id]
	if !exists {
		return fmt.Errorf("node with ID %s not found", id)
	}

	node.Title = title
	node.Content = content
	node.Tags = tags
	node.UpdatedAt = time.Now()

	// Update cache
	nm.updateCache(id, node)

	return nil
}

// DeleteNode removes a memory node (and optionally its children)
func (nm *NodeManager) DeleteNode(id string, recursive bool) error {
	nm.nodesMutex.Lock()
	defer nm.nodesMutex.Unlock()

	node, exists := nm.nodes[id]
	if !exists {
		return fmt.Errorf("node with ID %s not found", id)
	}

	// Handle children if recursive deletion is requested
	if recursive {
		for _, childID := range node.Children {
			nm.deleteNodeRecursive(childID)
		}
	} else {
		// Move children to parent (if any)
		if node.ParentID != "" {
			if parentNode, parentExists := nm.nodes[node.ParentID]; parentExists {
				parentNode.Children = append(parentNode.Children, node.Children...)
			}
		}
	}

	// Remove from parent's children list
	if node.ParentID != "" {
		if parentNode, parentExists := nm.nodes[node.ParentID]; parentExists {
			newChildren := []string{}
			for _, childID := range parentNode.Children {
				if childID != id {
					newChildren = append(newChildren, childID)
				}
			}
			parentNode.Children = newChildren
		}
	}

	// Remove the node itself
	delete(nm.nodes, id)
	delete(nm.cache, id)

	return nil
}

// deleteNodeRecursive recursively deletes a node and all its children
func (nm *NodeManager) deleteNodeRecursive(id string) {
	if node, exists := nm.nodes[id]; exists {
		// Recursively delete all children first
		for _, childID := range node.Children {
			nm.deleteNodeRecursive(childID)
		}

		// Remove from parent's children list
		if node.ParentID != "" {
			if parentNode, parentExists := nm.nodes[node.ParentID]; parentExists {
				newChildren := []string{}
				for _, childID := range parentNode.Children {
					if childID != id {
						newChildren = append(newChildren, childID)
					}
				}
				parentNode.Children = newChildren
			}
		}

		// Remove the node itself
		delete(nm.nodes, id)
		delete(nm.cache, id)
	}
}

// FindNodesByTag finds nodes that have specific tags
func (nm *NodeManager) FindNodesByTag(tag string) []*MemoryNode {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	var results []*MemoryNode

	for _, node := range nm.nodes {
		for _, nodeTag := range node.Tags {
			if nodeTag == tag {
				results = append(results, node)
				break
			}
		}
	}

	return results
}

// FindNodesByType finds nodes of a specific type
func (nm *NodeManager) FindNodesByType(nodeType NodeType) []*MemoryNode {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	var results []*MemoryNode

	for _, node := range nm.nodes {
		if node.Type == nodeType {
			results = append(results, node)
		}
	}

	return results
}

// FindNodesByTitle finds nodes with titles matching the query
func (nm *NodeManager) FindNodesByTitle(query string) []*MemoryNode {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	var results []*MemoryNode

	for _, node := range nm.nodes {
		if strings.Contains(strings.ToLower(node.Title), strings.ToLower(query)) ||
		   strings.Contains(strings.ToLower(node.Content), strings.ToLower(query)) {
			results = append(results, node)
		}
	}

	return results
}

// GetSubtree returns the subtree rooted at the given node ID
func (nm *NodeManager) GetSubtree(rootID string) ([]*MemoryNode, error) {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	rootNode, exists := nm.nodes[rootID]
	if !exists {
		return nil, fmt.Errorf("root node with ID %s not found", rootID)
	}

	var subtree []*MemoryNode
	nm.collectSubtree(rootNode, &subtree)

	return subtree, nil
}

// collectSubtree recursively collects all nodes in a subtree
func (nm *NodeManager) collectSubtree(node *MemoryNode, collection *[]*MemoryNode) {
	*collection = append(*collection, node)

	for _, childID := range node.Children {
		if childNode, exists := nm.nodes[childID]; exists {
			nm.collectSubtree(childNode, collection)
		}
	}
}

// GetPathToRoot returns the path from a node to the root
func (nm *NodeManager) GetPathToRoot(nodeID string) ([]*MemoryNode, error) {
	nm.nodesMutex.RLock()
	defer nm.nodesMutex.RUnlock()

	var path []*MemoryNode
	currentID := nodeID

	visited := make(map[string]bool) // Prevent infinite loops

	for currentID != "" {
		if visited[currentID] {
			return nil, fmt.Errorf("cycle detected in tree structure")
		}
		visited[currentID] = true

		node, exists := nm.nodes[currentID]
		if !exists {
			return nil, fmt.Errorf("node with ID %s not found", currentID)
		}

		path = append([]*MemoryNode{node}, path...) // Prepend to reverse the path

		currentID = node.ParentID
	}

	return path, nil
}

// SerializeNode serializes a node to JSON
func (nm *NodeManager) SerializeNode(node *MemoryNode) ([]byte, error) {
	return json.MarshalIndent(node, "", "  ")
}

// DeserializeNode deserializes a node from JSON
func (nm *NodeManager) DeserializeNode(data []byte) (*MemoryNode, error) {
	var node MemoryNode
	err := json.Unmarshal(data, &node)
	if err != nil {
		return nil, err
	}

	// Add to internal map if not already present
	nm.nodesMutex.Lock()
	defer nm.nodesMutex.Unlock()

	if _, exists := nm.nodes[node.ID]; !exists {
		nm.nodes[node.ID] = &node
	}

	return &node, nil
}