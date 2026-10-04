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

package provider

import "regexp"

// ParsedDataUrl holds the MIME type and base64 data extracted from a data URL.
type ParsedDataUrl struct {
	MimeType string
	Data     string
}

var dataURLRe = regexp.MustCompile(`^data:([^;,]+);base64,(.+)$`)

// ParseDataUrl extracts MIME type and base64 data from a data: URL.
// Returns nil if the input is not a valid data URL.
func ParseDataUrl(input string) *ParsedDataUrl {
	m := dataURLRe.FindStringSubmatch(input)
	if m == nil || m[1] == "" || m[2] == "" {
		return nil
	}
	return &ParsedDataUrl{MimeType: m[1], Data: m[2]}
}

// PartToText converts a MessagePart to a human-readable string representation.
func PartToText(part MessagePart) string {
	switch part.Type {
	case "text":
		return part.Text
	case "image_url":
		if part.ImageURL != nil {
			return "[image] " + part.ImageURL.URL
		}
		return "[image]"
	case "file":
		if part.File != nil {
			if part.File.URL != nil && *part.File.URL != "" {
				return "[file] " + *part.File.URL
			}
			if part.File.Filename != nil && *part.File.Filename != "" {
				return "[file] " + *part.File.Filename
			}
		}
		return "[file]"
	}
	return ""
}
