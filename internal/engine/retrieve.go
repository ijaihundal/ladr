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

// Package engine — on-demand retrieval helpers for the adaptive relikRead loop.
//
// The bounded loop itself lives in adaptive.go (ExecuteAdaptiveLoop). This file
// holds the retrieval execution helpers that the loop calls each turn when the
// LLM emits a relikRead request: executeRetrievalRequest, formatRetrievedDocs,
// and the RetrievedDoc / parseRetrievalRequest helpers.
package engine

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/ladr/ladr/internal/ladr"
	"github.com/ladr/ladr/internal/relik"
)

// RetrievedDoc is a single document fetched during a relikRead turn.
type RetrievedDoc struct {
	Path      string `json:"path"`
	Contents  string `json:"contents"`
	FromRound int    `json:"fromRound"`
}

// parseRetrievalRequest checks if the output matches the requestMatch schema.
// If yes, parses it as a RetrievalRequest.
func parseRetrievalRequest(output string, requestMatch json.RawMessage) (bool, *ladr.RetrievalRequest) {
	rawJSON, err := ExtractJSONFromOutput(output)
	if err != nil {
		return false, nil
	}

	var value any
	if err := json.Unmarshal(rawJSON, &value); err != nil {
		return false, nil
	}

	var schema any
	if err := json.Unmarshal(requestMatch, &schema); err != nil {
		return false, nil
	}

	if err := ValidateAgainstJSONSchema(value, schema); err != nil {
		return false, nil
	}

	// Parse as a retrieval request.
	var req ladr.RetrievalRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return false, nil
	}

	return true, &req
}

// executeRetrievalRequest fetches content for the requested files/subfolders/metatags/globs.
func executeRetrievalRequest(
	req *ladr.RetrievalRequest,
	target ladr.RelikTarget,
	store relik.Store,
	dedup map[string]bool,
	round int,
) []RetrievedDoc {
	var docs []RetrievedDoc

	// Mode: files — fetch specific files by path.
	for _, filePath := range req.Files {
		if dedup[filePath] {
			docs = append(docs, RetrievedDoc{Path: filePath, Contents: "(already in context)", FromRound: round})
			continue
		}
		dedup[filePath] = true
		content, err := store.ReadFile(target.Relik, target.Folder, filePath)
		if err != nil {
			docs = append(docs, RetrievedDoc{Path: filePath, Contents: "(not found)", FromRound: round})
		} else {
			docs = append(docs, RetrievedDoc{Path: filePath, Contents: content, FromRound: round})
		}
	}

	// Mode: subfolders — fetch subfolder indexes.
	for _, subfolderName := range req.Subfolders {
		key := "index:" + subfolderName
		if dedup[key] {
			continue
		}
		dedup[key] = true
		idx, err := store.ReadFolderIndex(target.Relik, subfolderName)
		if err != nil {
			docs = append(docs, RetrievedDoc{Path: "index:" + subfolderName, Contents: "(not found)", FromRound: round})
		} else {
			b, _ := json.MarshalIndent(idx, "", "  ")
			docs = append(docs, RetrievedDoc{Path: "index:" + subfolderName, Contents: string(b), FromRound: round})
		}
	}

	// Mode: metatag — query files by metatag value.
	if req.Metatag != nil {
		entries, err := store.ReadMetatagAcrossFolder(target.Relik, target.Folder, req.Metatag.Name)
		if err == nil {
			for _, entry := range entries {
				// Check contains filter.
				if req.Metatag.Contains != "" {
					if !metatagContains(entry.Value, req.Metatag.Contains) {
						continue
					}
				}
				// Fetch the file contents.
				content, err := store.ReadFile(target.Relik, target.Folder, entry.File)
				if err != nil {
					continue
				}
				key := entry.File
				if dedup[key] {
					continue
				}
				dedup[key] = true
				docs = append(docs, RetrievedDoc{Path: entry.File, Contents: content, FromRound: round})
			}
		}
	}

	// Mode: glob — match files by glob pattern.
	if req.Glob != "" {
		idx, err := store.ReadFolderIndex(target.Relik, target.Folder)
		if err == nil {
			for _, f := range idx.Files {
				matched, _ := path.Match(req.Glob, f.Name)
				if !matched {
					// Also try matching as path (folder/file).
					matched, _ = path.Match(req.Glob, target.Folder+"/"+f.Name)
				}
				if !matched {
					continue
				}
				if dedup[f.Name] {
					continue
				}
				dedup[f.Name] = true
				content, _ := store.ReadFile(target.Relik, target.Folder, f.Name)
				docs = append(docs, RetrievedDoc{Path: f.Name, Contents: content, FromRound: round})
			}
		}
	}

	return docs
}

// formatRetrievedDocs formats the retrieved documents as a readable string for injection.
func formatRetrievedDocs(docs []RetrievedDoc) string {
	var sb strings.Builder
	for i, doc := range docs {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("--- %s ---\n%s", doc.Path, doc.Contents))
	}
	return sb.String()
}

// metatagContains checks if a metatag value contains a substring.
func metatagContains(value any, substring string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, substring)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if strings.Contains(s, substring) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}
