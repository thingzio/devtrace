// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package score

// Grade returns a letter grade for the given score value.
func Grade(score float64) string {
	switch {
	case score >= 0.93:
		return "A+"
	case score >= 0.87:
		return "A"
	case score >= 0.83:
		return "A-"
	case score >= 0.77:
		return "B+"
	case score >= 0.73:
		return "B"
	case score >= 0.67:
		return "B-"
	case score >= 0.63:
		return "C+"
	case score >= 0.57:
		return "C"
	case score >= 0.53:
		return "C-"
	case score >= 0.47:
		return "D+"
	case score >= 0.43:
		return "D"
	case score >= 0.37:
		return "D-"
	default:
		return "F"
	}
}
