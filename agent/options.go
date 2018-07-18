//  Copyright (c) 2018 Cisco and/or its affiliates.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at:
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package agent

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"syscall"

	"github.com/ligato/cn-infra/infra"
	"github.com/ligato/cn-infra/logging"
	"github.com/ligato/cn-infra/logging/logrus"
)

// Variables set by the compiler using ldflags
var (
	// BuildVersion describes version for the build. It is usually set using `git describe --always --tags --dirty`.
	BuildVersion = "dev"
	// BuildDate describes time of the build.
	BuildDate string
	// CommitHash describes commit hash for the build.
	CommitHash string
)

var infraLogger = logrus.NewLogger("agent")

func init() {
	if os.Getenv("DEBUG_INFRA") != "" {
		infraLogger.SetLevel(logging.DebugLevel)
		infraLogger.Debugf("infra debug logger enabled")
	}
}

// Options specifies option list for the Agent
type Options struct {
	QuitSignals []os.Signal
	QuitChan    chan struct{}
	ctx         context.Context

	Plugins   []infra.Plugin
	pluginMap map[infra.Plugin]struct{}
}

func newOptions(opts ...Option) Options {
	opt := Options{
		QuitSignals: []os.Signal{
			os.Interrupt,
			syscall.SIGTERM,
			syscall.SIGKILL,
		},
		pluginMap: make(map[infra.Plugin]struct{}),
	}

	for _, o := range opts {
		o(&opt)
	}

	return opt
}

// Option is a function that operates on an Agent's Option
type Option func(*Options)

// Version returns an Option that sets the version of the Agent to the entered string
func Version(buildVer, buildDate, commitHash string) Option {
	return func(o *Options) {
		BuildVersion = buildVer
		BuildDate = buildDate
		CommitHash = commitHash
	}
}

// Context returns an Option that sets the context for the Agent
func Context(ctx context.Context) Option {
	return func(o *Options) {
		o.ctx = ctx
	}
}

// QuitSignals returns an Option that will set signals which stop Agent
func QuitSignals(sigs ...os.Signal) Option {
	return func(o *Options) {
		o.QuitSignals = sigs
	}
}

// QuitOnClose returns an Option that will set channel which stops Agent on close
func QuitOnClose(ch chan struct{}) Option {
	return func(o *Options) {
		o.QuitChan = ch
	}
}

// Plugins creates an Option that adds a list of Plugins to the Agent's Plugin list
func Plugins(plugins ...infra.Plugin) Option {
	return func(o *Options) {
		o.Plugins = append(o.Plugins, plugins...)
	}
}

// AllPlugins creates an Option that adds all of the nested
// plugins recursively to the Agent's plugin list.
func AllPlugins(plugins ...infra.Plugin) Option {
	return func(o *Options) {
		infraLogger.Debugf("AllPlugins with %d plugins", len(plugins))

		for _, plugin := range plugins {
			infraLogger.Debugf("recursively searching for deps in: %v", plugin)

			plugins, err := findPlugins(reflect.ValueOf(plugin), o.pluginMap)
			if err != nil {
				panic(err)
			}
			o.Plugins = append(o.Plugins, plugins...)
			typ := reflect.TypeOf(plugin)
			infraLogger.Debugf("recursively found %d plugins inside %v", len(plugins), typ)
			for _, plug := range plugins {
				infraLogger.Debugf(" - plugin: %v (%v)", plug, reflect.TypeOf(plug))
			}

			// TODO: set plugin name to typ.String() if empty
			/*p, ok := plugin.(core.PluginNamed)
			if !ok {
				p = core.NamePlugin(typ.String(), plugin)
			}*/

			o.Plugins = append(o.Plugins, plugin)
		}
	}
}

func findPlugins(val reflect.Value, uniqueness map[infra.Plugin]struct{}, x ...int) (
	res []infra.Plugin, err error,
) {
	n := 0
	if len(x) > 0 {
		n = x[0]
	}
	var logf = func(f string, a ...interface{}) {
		for i := 0; i < n; i++ {
			f = "\t" + f
		}
		//infraLogger.Debugf(f, a...)
		fmt.Printf(f+"\n", a...)
	}

	typ := val.Type()

	logf("=> %v (%v)", typ, typ.Kind())
	defer logf("== %v ", typ)

	if typ.Kind() == reflect.Interface {
		if val.IsNil() {
			logf(" - val is nil")
			return nil, nil
		}
		val = val.Elem()
		typ = val.Type()
		//logf(" - interface to elem: %v (%v)", typ, val.Kind())
	}

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
		//logrus.DefaultLogger().Debug(" - typ ptr kind: ", typ)
	}
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
		//logrus.DefaultLogger().Debug(" - val ptr kind: ", val)
	}

	if !val.IsValid() {
		logf(" - val is invalid")
		return nil, nil
	}

	if typ.Kind() != reflect.Struct {
		logf(" - is not a struct: %v %v", typ.Kind(), val.Kind())
		return nil, nil
	}

	//logf(" -> checking %d fields", typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// PkgPath is empty for exported fields
		if exported := field.PkgPath == ""; !exported {
			logf(" - skip unexported: %v", field.Name)
			continue
		}

		fieldVal := val.Field(i)

		logf("-> field %d: %v - %v (%v)", i, field.Name, field.Type, fieldVal.Kind())

		var fieldPlug infra.Plugin

		plug, implementsPlugin := isFieldPlugin(field, fieldVal)
		if implementsPlugin {
			if plug == nil {
				logf(" - found nil plugin: %v", field.Name)
				continue
			}

			_, found := uniqueness[plug]
			if found {
				logf(" - found duplicate plugin: %v %v", field.Name, field.Type)
				continue
			}

			uniqueness[plug] = struct{}{}
			/*p, ok := plug.(core.PluginNamed)
			if !ok {
				p = core.NamePlugin(field.Name, plug)
			}*/
			fieldPlug = plug

			logf(" + FOUND PLUGIN: %v - %v (%v)", plug.String(), field.Name, field.Type)

			/*var pp core.Plugin = plug
			if np, ok := p.(*core.NamedPlugin); ok {
				pp = np.Plugin
			}*/
		}

		// do recursive inspection only for plugins and fields Deps
		if fieldPlug != nil || (field.Name == "Deps" && fieldVal.Kind() == reflect.Struct) {
			//var l []core.PluginNamed
			// try to inspect structure recursively
			l, err := findPlugins(fieldVal, uniqueness, n+1)
			if err != nil {
				logf(" - Bad field: %v %v", field.Name, err)
				continue
			}
			//logf(" - listed %v plugins from %v (%v)", len(l), field.Name, field.Type)
			res = append(res, l...)
		}

		if fieldPlug != nil {
			res = append(res, fieldPlug)
		}
	}

	logf("<- got %d plugins", len(res))

	return res, nil
}

var pluginType = reflect.TypeOf((*infra.Plugin)(nil)).Elem()

func isFieldPlugin(field reflect.StructField, fieldVal reflect.Value) (infra.Plugin, bool) {
	//logrus.DefaultLogger().Debugf(" - is field plugin: %v (%v) %v", field.Type, fieldVal.Kind(), fieldVal)

	switch fieldVal.Kind() {
	case reflect.Struct:
		ptrType := reflect.PtrTo(fieldVal.Type())
		if ptrType.Implements(pluginType) {
			if fieldVal.CanAddr() {
				if plug, ok := fieldVal.Addr().Interface().(infra.Plugin); ok {
					return plug, true
				}
			}
			return nil, true
		}
	case reflect.Ptr, reflect.Interface:
		if plug, ok := fieldVal.Interface().(infra.Plugin); ok {
			if fieldVal.IsNil() {
				return nil, true
			}
			return plug, true
		} else {
			//logrus.DefaultLogger().Debugf(" - does not implement Plugin: %v", field.Type.Implements(pluginType))
		}
	}

	return nil, false
}
