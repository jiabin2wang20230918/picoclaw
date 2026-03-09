// Package tree implements vector-based similarity search for semantic memory retrieval
package tree

import (
	"math"
	"strings"
)

// VectorStore manages embeddings and similarity search
type VectorStore struct {
	nodes       *NodeManager
	index       map[string][]float32 // node ID -> embedding vector
	vocab       map[string]int       // word -> index in vocabulary
	docFreq     map[string]int       // document frequency for TF-IDF
	documents   []string             // document texts for vocabulary building
}

// NewVectorStore creates a new vector store
func NewVectorStore(nodes *NodeManager) *VectorStore {
	return &VectorStore{
		nodes:     nodes,
		index:     make(map[string][]float32),
		vocab:     make(map[string]int),
		docFreq:   make(map[string]int),
		documents: []string{},
	}
}

// AddOrUpdateEmbedding adds or updates a node's embedding
func (vs *VectorStore) AddOrUpdateEmbedding(nodeID string, text string) {
	embedding := vs.textToEmbedding(text)
	vs.index[nodeID] = embedding
}

// textToEmbedding converts text to a numerical vector using TF-IDF
func (vs *VectorStore) textToEmbedding(text string) []float32 {
	// For this implementation, we'll use a simple TF-IDF approach
	// In a real implementation, you might use a pre-trained model

	// Tokenize text
	tokens := vs.tokenize(text)

	// Count term frequencies in this document
	tf := make(map[string]float32)
	for _, token := range tokens {
		tf[token]++
	}

	// Add document to corpus for IDF calculation
	vs.documents = append(vs.documents, text)

	// Build vocabulary if not already done
	if len(vs.vocab) == 0 {
		vs.buildVocabulary()
	}

	// Calculate TF-IDF vector
	vector := make([]float32, len(vs.vocab))
	for token, tfVal := range tf {
		if idx, exists := vs.vocab[token]; exists {
			idfVal := vs.inverseDocumentFrequency(token)
			vector[idx] = tfVal * idfVal
		}
	}

	return vector
}

// tokenize splits text into tokens
func (vs *VectorStore) tokenize(text string) []string {
	// Simple tokenization by space and punctuation
	text = strings.ToLower(text)
	tokens := []string{}

	var currentToken strings.Builder
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			currentToken.WriteRune(r)
		} else {
			token := currentToken.String()
			if token != "" && !vs.isStopWord(token) {
				tokens = append(tokens, token)
			}
			currentToken.Reset()
		}
	}

	// Handle the last token
	if currentToken.Len() > 0 {
		token := currentToken.String()
		if token != "" && !vs.isStopWord(token) {
			tokens = append(tokens, token)
		}
	}

	return tokens
}

// isStopWord checks if a word is a common stop word (reusing logic from index.go)
func (vs *VectorStore) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "up": true, "about": true,
		"into": true, "through": true, "during": true, "before": true, "after": true,
		"above": true, "below": true, "between": true, "among": true, "is": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "can": true, "this": true,
		"that": true, "these": true, "those": true, "i": true, "you": true,
		"he": true, "she": true, "it": true, "we": true, "they": true,
		"me": true, "him": true, "her": true, "us": true, "them": true,
	}

	_, exists := stopWords[word]
	return exists
}

// buildVocabulary builds the vocabulary from all documents
func (vs *VectorStore) buildVocabulary() {
	allTokens := make(map[string]bool)

	for _, doc := range vs.documents {
		tokens := vs.tokenize(doc)
		seenInDoc := make(map[string]bool)

		for _, token := range tokens {
			allTokens[token] = true
			if !seenInDoc[token] {
				vs.docFreq[token]++
				seenInDoc[token] = true
			}
		}
	}

	// Assign indices to vocabulary
	idx := 0
	for token := range allTokens {
		vs.vocab[token] = idx
		idx++
	}
}

// inverseDocumentFrequency calculates IDF for a term
func (vs *VectorStore) inverseDocumentFrequency(term string) float32 {
	df, exists := vs.docFreq[term]
	if !exists {
		return 1.0 // Term not seen in corpus
	}

	totalDocs := len(vs.documents)
	if df == 0 {
		return 1.0
	}

	return float32(math.Log(float64(totalDocs) / float64(df)))
}

// cosineSimilarity calculates cosine similarity between two vectors
func (vs *VectorStore) cosineSimilarity(v1, v2 []float32) float64 {
	if len(v1) != len(v2) {
		return 0.0
	}

	dotProduct := 0.0
	magnitude1 := 0.0
	magnitude2 := 0.0

	for i := 0; i < len(v1); i++ {
		dotProduct += float64(v1[i] * v2[i])
		magnitude1 += math.Pow(float64(v1[i]), 2)
		magnitude2 += math.Pow(float64(v2[i]), 2)
	}

	magnitude1 = math.Sqrt(magnitude1)
	magnitude2 = math.Sqrt(magnitude2)

	if magnitude1 == 0 || magnitude2 == 0 {
		return 0.0
	}

	return dotProduct / (magnitude1 * magnitude2)
}

// SearchBySimilarity finds nodes similar to the query text
func (vs *VectorStore) SearchBySimilarity(query string, topK int) []*SearchResult {
	queryEmbedding := vs.textToEmbedding(query)

	var results []*SearchResult

	// Calculate similarity to all indexed nodes
	for nodeID, embedding := range vs.index {
		similarity := vs.cosineSimilarity(queryEmbedding, embedding)

		if similarity > 0 { // Only include results with positive similarity
			if node, exists := vs.nodes.GetNode(nodeID); exists {
				results = append(results, &SearchResult{
					Node:  node,
					Score: similarity,
				})
			}
		}
	}

	// Sort results by similarity
	vs.sortResultsByScore(results)

	// Limit to top K results
	if len(results) > topK {
		results = results[:topK]
	}

	return results
}

// sortResultsByScore sorts search results by score in descending order
func (vs *VectorStore) sortResultsByScore(results []*SearchResult) {
	// Simple bubble sort - for production, consider using sort.Slice
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Score < results[j].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}