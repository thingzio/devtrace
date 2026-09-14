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

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thingzio/devtrace/pkg/logging"
	"github.com/thingzio/devtrace/pkg/server"
	"github.com/thingzio/devtrace/pkg/version"
)

func main() {
	v := version.Get()
	logging.SetupLogger(v.Version)

	slog.Info("starting devtrace-site",
		"version", v.Version,
		"commit", v.Commit,
		"date", v.Date,
	)

	os.Exit(run(v))
}

func run(v version.Info) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, server.Options{Version: v.Version, Commit: v.Commit, Date: v.Date}); err != nil {
		slog.Error("fatal error", "error", err)
		return 1
	}
	return 0
}
