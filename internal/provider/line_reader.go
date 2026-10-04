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

import (
	"bufio"
	"io"
)

// lineReader wraps a reader with bufio for line-by-line reading.
type lineReader struct {
	r *bufio.Reader
}

func newLineReader(r io.Reader) *lineReader {
	return &lineReader{r: bufio.NewReader(r)}
}

// ReadLine reads a single line (including the newline). Returns the raw line
// and any error. io.EOF is returned when no more data is available.
func (lr *lineReader) ReadLine() ([]byte, error) {
	line, err := lr.r.ReadBytes('\n')
	if len(line) > 0 {
		return line, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, io.EOF
}
