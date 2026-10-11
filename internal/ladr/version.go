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

package ladr

// CurrentGrammarVersion is the LADR grammar version implemented by this
// engine. Ladder configs declare their target grammar via ladrVersion at
// the top level; if omitted, the config is treated as the current version
// (backward compatibility for pre-versioning configs).
//
// Versioning follows major.minor:
//   - "0.1" — initial grammar: steps, fields, knobs, continueIf/if gates,
//     reliks, writes, retrieve, dynamic steps, execution budget, matches
//     regex conditions, and/or/not compound conditions.
//
// Patches (0.1.0 → 0.1.3) fix engine bugs without changing the grammar;
// they are invisible to ladder authors and do not appear in ladrVersion.
const CurrentGrammarVersion = "0.1"
