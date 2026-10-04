// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Package indexer — the content-addressed chunk store: everything a session
// ever received, stored lossless, deduplicated by identity.
//
// Identity, not similarity: prompt caching is accurate because it is exact.
// The indexer steals the trick — the SHA of a chunk IS its identity, so
// repeat content across turns (or sessions) costs zero new storage. LADR
// assembles context itself, so we index the DELTA (input as given), never
// the re-inflated prompt; what duplication remains, hashing removes.
package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Chunk is one content-addressed object.
type Chunk struct {
	Hash    string
	Content string
	Tokens  int
}

// DefaultChunkTokens is the v1 chunk ceiling when config omits one.
const DefaultChunkTokens = 512

// estimateTokens matches the relik store's chars/4 heuristic — the same
// yardstick everywhere so limits and chunking agree.
func estimateTokens(s string) int { return len(s) / 4 }

// HashContent returns the chunk identity for a content string.
func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ChunkText splits text into chunks deterministically: same text → same
// boundaries → same hashes, always. Paragraphs pack greedily up to the token
// cap; an oversized paragraph hard-splits on word boundaries. Overlap (when
// > 0) carries whole trailing paragraphs into the next chunk.
func ChunkText(text string, chunkTokens, overlap int) []Chunk {
	if chunkTokens <= 0 {
		chunkTokens = DefaultChunkTokens
	}
	if overlap < 0 {
		overlap = 0
	}
	maxChars := chunkTokens * 4

	paragraphs := strings.Split(text, "\n\n")
	// Hard-split any paragraph that alone exceeds the cap.
	var units []string
	for _, p := range paragraphs {
		if len(p) <= maxChars {
			units = append(units, p)
			continue
		}
		words := strings.Fields(p)
		var buf []string
		n := 0
		for _, w := range words {
			// +1 for the joining space.
			if n+len(w)+1 > maxChars && len(buf) > 0 {
				units = append(units, strings.Join(buf, " "))
				buf, n = nil, 0
			}
			buf = append(buf, w)
			n += len(w) + 1
		}
		if len(buf) > 0 {
			units = append(units, strings.Join(buf, " "))
		}
	}

	var chunks []Chunk
	var cur []string
	curChars := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		content := strings.Join(cur, "\n\n")
		chunks = append(chunks, Chunk{
			Hash:    HashContent(content),
			Content: content,
			Tokens:  estimateTokens(content),
		})
		// Seed the next chunk with trailing paragraphs (whole units only).
		if overlap > 0 {
			carried, carriedChars := 0, 0
			var seed []string
			for i := len(cur) - 1; i >= 0; i-- {
				if i < len(cur)-1 && (carried >= overlap || carriedChars+len(cur[i])+2 > maxChars) {
					break
				}
				seed = append([]string{cur[i]}, seed...)
				carried++
				carriedChars += len(cur[i]) + 2
			}
			if len(seed) == len(cur) {
				// Overlap would repeat the whole chunk — carry all but the first.
				seed = seed[1:]
			}
			if len(seed) > 0 {
				cur = append([]string{}, seed...)
				curChars = 0
				for _, s := range cur {
					curChars += len(s) + 2
				}
				return
			}
		}
		cur, curChars = nil, 0
	}
	for _, u := range units {
		if len(u) == 0 {
			continue
		}
		if curChars+len(u)+2 > maxChars && len(cur) > 0 {
			flush()
		}
		cur = append(cur, u)
		curChars += len(u) + 2
	}
	flush()

	// A lone oversized unit (post-split this means maxChars-boundary words)
	// still lands as its own chunk — lossless by law.
	if len(chunks) == 0 && strings.TrimSpace(text) != "" {
		content := text
		chunks = append(chunks, Chunk{Hash: HashContent(content), Content: content, Tokens: estimateTokens(content)})
	}
	return chunks
}
