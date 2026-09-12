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

package server

import "testing"

func TestWebhookNotifyNonBlocking(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{}, 1)

	// Fill the channel
	ch <- struct{}{}

	// Second send should not block
	notifyInstallationChange(ch)

	// Should still have exactly one signal
	select {
	case <-ch:
	default:
		t.Error("channel should have had a signal")
	}
}

func TestWebhookNotifyNil(t *testing.T) {
	t.Parallel()
	// Should not panic with nil channel
	notifyInstallationChange(nil)
}
