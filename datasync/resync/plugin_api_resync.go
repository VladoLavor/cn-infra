// Copyright (c) 2017 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resync

import (
	"github.com/ligato/cn-infra/core"
)

// PluginID used in the Agent Core flavors
const PluginID core.PluginName = "RESYNC_ORCH"

//TODO move this API under datasync package
//FIXME avoid global API

// Register function is supposed to be called in Init() by all VPP Agent plugins.
// Those plugins will use Registration.StatusChan() to listen
// The plugins are supposed to load current state of their objects when newResync() is called.
func Register(resyncName string) Registration {
	return plugin().Register(resyncName)
}

// ReportError is called by the Plugins when the binary api call was not successful.
// Based on that the Resync Orchestrator starts the Resync.
func ReportError(name core.PluginName, err error) {
	//TODO plugin().ReportError(name, err)
}
