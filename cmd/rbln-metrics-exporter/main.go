/*
Copyright 2025-2026 Rebellions Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"log/slog"
	"os"

	appcmd "github.com/rebellions-sw/rbln-metrics-exporter/internal/cmd"
	"github.com/rebellions-sw/rbln-metrics-exporter/internal/logging"
)

func main() {
	logging.SetupFromEnv()
	app := appcmd.NewApp()
	if err := app.Execute(); err != nil {
		slog.Error("Command execution failed", "err", err)
		os.Exit(1)
	}
}
