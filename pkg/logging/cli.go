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

package logging

import (
	"log/slog"
	"os"

	"github.com/thingzio/devtrace/pkg/config"
)

// SetupLogger configures the default slog logger with JSON output.
// If version is non-empty, it is included in every log entry automatically.
func SetupLogger(version string) {
	level := slog.LevelInfo
	if config.DebugEnabled() {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	if version != "" {
		logger = logger.With("version", version)
	}
	slog.SetDefault(logger)
}
