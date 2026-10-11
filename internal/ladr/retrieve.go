// Redeo - the programming layer for inference-time reasoning.
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

// Package ladr — retrieval request types used by the adaptive relikRead loop.
//
// The old standalone retrieve: step key and the Retrieve struct have been
// removed (replaced by the flat relikRead: step key — see adaptive.go). What
// remains here are the request-parsing types that the engine uses to parse
// the LLM's relikRead call output.
package ladr

// RetrievalRequestMode enumerates the four retrieval modes.
type RetrievalRequestMode string

const (
	RetrieveFiles      RetrievalRequestMode = "files"
	RetrieveSubfolders RetrievalRequestMode = "subfolders"
	RetrieveMetatag    RetrievalRequestMode = "metatag"
	RetrieveGlob       RetrievalRequestMode = "glob"
)

// RetrievalRequest is the parsed LLM output when it matches the relikRead
// requestMatch schema. Multiple modes can combine in one request.
type RetrievalRequest struct {
	Files      []string             `json:"files,omitempty"`
	Subfolders []string             `json:"subfolders,omitempty"`
	Metatag    *RetrievalMetatagReq `json:"metatag,omitempty"`
	Glob       string               `json:"glob,omitempty"`
}

// RetrievalMetatagReq is the metatag query mode.
type RetrievalMetatagReq struct {
	Name     string `json:"name"`
	Contains string `json:"contains,omitempty"`
}
