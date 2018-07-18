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

package msgsync

import (
	"errors"

	"github.com/golang/protobuf/proto"
	"github.com/ligato/cn-infra/config"
	"github.com/ligato/cn-infra/datasync"
	"github.com/ligato/cn-infra/infra"
	"github.com/ligato/cn-infra/logging"
	"github.com/ligato/cn-infra/messaging"
)

// Plugin implements KeyProtoValWriter that propagates protobuf messages
// to a particular topic (unless the messaging.Mux is not disabled).
type Plugin struct {
	Deps // inject

	Cfg
	adapter messaging.ProtoPublisher
}

// Deps groups dependencies injected into the plugin so that they are
// logically separated from other plugin fields.
type Deps struct {
	infra.PluginName                      // inject
	Log              logging.PluginLogger // inject
	config.PluginConfig
	Messaging messaging.Mux // inject
}

// Cfg groups configurations fields. It can be extended with other fields
// (such as sync/async, partition...).
type Cfg struct {
	Topic string
}

// Init does nothing.
func (plugin *Plugin) Init() error {
	return nil
}

// AfterInit uses provided MUX connection to build new publisher.
func (plugin *Plugin) AfterInit() error {
	if !plugin.Messaging.Disabled() {
		cfg := plugin.Cfg
		plugin.PluginConfig.GetValue(&cfg)

		if cfg.Topic != "" {
			var err error
			plugin.adapter, err = plugin.Messaging.NewSyncPublisher("msgsync-connection", cfg.Topic)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Put propagates this call to a particular messaging Publisher.
//
// This method is supposed to be called in PubPlugin.AfterInit() or later (even from different go routine).
func (plugin *Plugin) Put(key string, data proto.Message, opts ...datasync.PutOption) error {
	if plugin.Messaging.Disabled() {
		return nil
	}

	if plugin.adapter != nil {
		return plugin.adapter.Put(key, data, opts...)
	}

	return errors.New("Transport adapter is not ready yet. (Probably called before AfterInit)")
}

// Close resources.
func (plugin *Plugin) Close() error {
	return nil
}
