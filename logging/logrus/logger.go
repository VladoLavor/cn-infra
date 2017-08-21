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

package logrus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"regexp"
	"sync/atomic"

	lg "github.com/Sirupsen/logrus"
	"github.com/ligato/cn-infra/logging"
	"github.com/satori/go.uuid"
)

const DefaultLoggerName = "defaultLogger"
var defaultLogger *Logger

// initializes global Logrus logger. Please notice, that recommended
// approach is to create a custom logger.
func init() {
	defaultLogger = NewLogger(DefaultLoggerName)
}

// DefaultLogger returns a global Logrus logger. Please notice, that recommended
// approach is to create a custom logger.
func DefaultLogger() *Logger {
	return defaultLogger
}

// Logger is wrapper of Logrus logger. In addition to Logrus functionality it
// allows to define static log fields that are added to all subsequent log entries. It also automatically
// appends file name and line where the log is coming from. In order to distinguish logs from different
// go routines a tag (number that is based on the stack address) is computed. To achieve better readability
// numeric value of a tag can be replaced by a string using SetTag function.
type Logger struct {
	sync.RWMutex
	std          *lg.Logger
	depth        int
	littleBuf    sync.Pool
	tagmap       map[uint64]string
	staticFields map[string]interface{}
	name         string
}

// NewLogger is a constructor creates instances of named logger.
// This constructor is called from logRegistry which is useful
// when log levels needs to be changed by management API (such as REST)
//
// Example:
//
//    logger := NewLogger("loggerXY")
//    logger.Info()
//
func NewLogger(name string) *Logger {
	logger := &Logger{
		std:    lg.New(),
		depth:  2,
		tagmap: make(map[uint64]string, 64),
		name:   name,
	}

	logger.InitTag("00000000")

	logger.littleBuf.New = func() interface{} {
		buf := make([]byte, 64)
		return &buf
	}
	return logger
}

// Fields is a type for structured log entries.
type Fields map[string]interface{}

var validLoggerName = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`).MatchString

func checkLoggerName(name string) error {
	if !validLoggerName(name) {
		return fmt.Errorf("logger name can contain only alphanum characters, dash and comma")
	}
	return nil
}

// NewJSONFormatter creates a new instance of JSONFormatter
func NewJSONFormatter() *lg.JSONFormatter {
	return &lg.JSONFormatter{}
}

// NewTextFormatter creates a new instance of TextFormatter
func NewTextFormatter() *lg.TextFormatter {
	return &lg.TextFormatter{}
}

// NewCustomFormatter creates a new instance of CustomFormatter
func NewCustomFormatter() *CustomFormatter {
	return &CustomFormatter{}
}

// StandardLogger returns internally used Logrus logger
func (logger *Logger) StandardLogger() *lg.Logger {
	return logger.std
}

// GetLineInfo returns the location (filename + linenumber) of the caller.
func (logger *Logger) GetLineInfo(depth int) string {
	_, f, l, ok := runtime.Caller(depth)
	if !ok {
		f = "???"
		l = 0
	}
	base := path.Base(f)
	dir := path.Dir(f)
	folders := strings.Split(dir, "/")
	parent := ""
	if folders != nil {
		parent = folders[len(folders)-1] + "/"
	}
	file := parent + base
	line := strconv.Itoa(l)
	return fmt.Sprintf("%s(%s)", file, line)
}

// InitTag sets the tag for the main thread.
func (logger *Logger) InitTag(tag ...string) {
	logger.Lock()
	defer logger.Unlock()
	var t string
	if tag != nil || len(tag) > 0 {
		t = tag[0]
	} else {
		t = uuid.NewV4().String()[0:8]
	}
	logger.tagmap[0] = t
}

// GetTag returns the tag identifying the caller's go routine.
func (logger *Logger) GetTag() string {
	logger.RLock()
	defer logger.RUnlock()
	ti := logger.curGoroutineID()
	tag, ok := logger.tagmap[ti]
	if !ok {
		tag = logger.tagmap[0]
	}

	return tag
}

// SetTag allows to define a string tag for the current go routine. Otherwise
// numeric identification is used.
func (logger *Logger) SetTag(tag ...string) {
	logger.Lock()
	defer logger.Unlock()
	ti := logger.curGoroutineID()
	var t string
	if tag != nil || len(tag) > 0 {
		t = tag[0]
	} else {
		t = uuid.NewV4().String()[0:8]
	}
	logger.tagmap[ti] = t
}

// ClearTag removes the previously set string tag for the current go routine.
func (logger *Logger) ClearTag() {
	logger.Lock()
	defer logger.Unlock()
	ti := logger.curGoroutineID()
	delete(logger.tagmap, ti)
}

// SetStaticFields sets a map of fields that will be part of the each subsequent
// log entry of the logger
func (logger *Logger) SetStaticFields(fields map[string]interface{}) {
	logger.Lock()
	defer logger.Unlock()
	logger.staticFields = fields
}

// GetStaticFields returns currently set map of static fields - key-value pairs
// that are automatically added into log entry
func (logger *Logger) GetStaticFields() map[string]interface{} {
	logger.Lock()
	defer logger.Unlock()
	return logger.staticFields
}

// GetName return the logger name
func (logger *Logger) GetName() string {
	return logger.name
}

// SetOutput sets the standard logger output.
func (logger *Logger) SetOutput(out io.Writer) {
	logger.Lock()
	defer logger.Unlock()
	logger.std.Out = out
}

// SetFormatter sets the standard logger formatter.
func (logger *Logger) SetFormatter(formatter lg.Formatter) {
	logger.Lock()
	defer logger.Unlock()
	logger.std.Formatter = formatter
}

// SetLevel sets the standard logger level.
func (logger *Logger) SetLevel(level logging.LogLevel) {
	logger.Lock()
	defer logger.Unlock()
	switch level {
	case logging.PanicLevel:
		logger.std.Level = lg.PanicLevel
	case logging.FatalLevel:
		logger.std.Level = lg.FatalLevel
	case logging.ErrorLevel:
		logger.std.Level = lg.ErrorLevel
	case logging.WarnLevel:
		logger.std.Level = lg.WarnLevel
	case logging.InfoLevel:
		logger.std.Level = lg.InfoLevel
	case logging.DebugLevel:
		logger.std.Level = lg.DebugLevel
	}

}

// GetLevel returns the standard logger level.
func (logger *Logger) GetLevel() logging.LogLevel {
	l := lg.Level(atomic.LoadUint32((*uint32)(&logger.std.Level)))
	switch l {
	case lg.PanicLevel:
		return logging.PanicLevel
	case lg.FatalLevel:
		return logging.FatalLevel
	case lg.ErrorLevel:
		return logging.ErrorLevel
	case lg.WarnLevel:
		return logging.WarnLevel
	case lg.InfoLevel:
		return logging.InfoLevel
	case lg.DebugLevel:
		return logging.DebugLevel
	default:
		return logging.DebugLevel
	}
}

// AddHook adds a hook to the standard logger hooks.
func (logger *Logger) AddHook(hook lg.Hook) {
	logger.Lock()
	defer logger.Unlock()
	logger.std.Hooks.Add(hook)
}

func (logger *Logger) withField(key string, value interface{}, depth ...int) *LogMsg {
	d := 1
	if depth != nil && len(depth) > 0 {
		d += depth[0]
	}
	return logger.withFields(Fields{key: value}, d)
}

// WithField creates an entry from the standard logger and adds a field to
// it. If you want multiple fields, use `WithFields`.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the LogMsg it returns.
func (logger *Logger) WithField(key string, value interface{}) logging.LogWithLevel {
	return logger.withField(key, value, 1)
}

func (logger *Logger) withFields(fields Fields, depth ...int) *LogMsg {
	d := logger.depth
	if depth != nil && len(depth) > 0 {
		d += depth[0]
	}

	static := logger.GetStaticFields()

	f := make(lg.Fields, len(fields)+len(static))

	for k, v := range static {
		f[k] = v
	}

	for k, v := range fields {
		f[k] = v
	}
	/*TODO
	if _, ok := f[tagKey]; !ok {
		f[tagKey] = logger.GetTag()
	}
	if _, ok := f[locKey]; !ok {
		f[locKey] = logger.GetLineInfo(d)
	}
	*/
	entry := logger.std.WithFields(f)
	return &LogMsg{
		logger: logger,
		Entry:  entry,
	}
}

// WithFields creates an entry from the standard logger and adds multiple
// fields to it. This is simply a helper for `WithField`, invoking it
// once for each field.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the LogMsg it returns.
func (logger *Logger) WithFields(fields map[string]interface{}) logging.LogWithLevel {
	return logger.withFields(Fields(fields), 1)
}

func (logger *Logger) header(depth int) *LogMsg {
	t := logger.GetTag()
	l := logger.GetLineInfo(logger.depth + depth)
	e := logger.withFields(Fields{
		tagKey: t,
		locKey: l,
	}, 2)
	return e
}

// Debug logs a message at level Debug on the standard logger.
func (logger *Logger) Debug(args ...interface{}) {
	logger.header(1).Debug(args...)
}

// Print logs a message at level Info on the standard logger.
func (logger *Logger) Print(args ...interface{}) {
	logger.std.Print(args...)
}

// Info logs a message at level Info on the standard logger.
func (logger *Logger) Info(args ...interface{}) {
	logger.header(1).Info(args...)
}

// Warn logs a message at level Warn on the standard logger.
func (logger *Logger) Warn(args ...interface{}) {
	logger.header(1).Warn(args...)
}

// Warning logs a message at level Warn on the standard logger.
func (logger *Logger) Warning(args ...interface{}) {
	logger.header(1).Warning(args...)
}

// Error logs a message at level Error on the standard logger.
func (logger *Logger) Error(args ...interface{}) {
	logger.header(1).Error(args...)
}

// Panic logs a message at level Panic on the standard logger.
func (logger *Logger) Panic(args ...interface{}) {
	logger.header(1).Panic(args...)
}

// Fatal logs a message at level Fatal on the standard logger.
func (logger *Logger) Fatal(args ...interface{}) {
	logger.header(1).Fatal(args...)
}

// Debugf logs a message at level Debug on the standard logger.
func (logger *Logger) Debugf(format string, args ...interface{}) {
	logger.header(1).Debugf(format, args...)
}

// Printf logs a message at level Info on the standard logger.
func (logger *Logger) Printf(format string, args ...interface{}) {
	logger.header(1).Printf(format, args...)
}

// Infof logs a message at level Info on the standard logger.
func (logger *Logger) Infof(format string, args ...interface{}) {
	logger.header(1).Infof(format, args...)
}

// Warnf logs a message at level Warn on the standard logger.
func (logger *Logger) Warnf(format string, args ...interface{}) {
	logger.header(1).Warnf(format, args...)
}

// Warningf logs a message at level Warn on the standard logger.
func (logger *Logger) Warningf(format string, args ...interface{}) {
	logger.header(1).Warningf(format, args...)
}

// Errorf logs a message at level Error on the standard logger.
func (logger *Logger) Errorf(format string, args ...interface{}) {
	logger.header(1).Errorf(format, args...)
}

// Panicf logs a message at level Panic on the standard logger.
func (logger *Logger) Panicf(format string, args ...interface{}) {
	logger.header(1).Panicf(format, args...)
}

// Fatalf logs a message at level Fatal on the standard logger.
func (logger *Logger) Fatalf(format string, args ...interface{}) {
	logger.header(1).Fatalf(format, args...)
}

// Debugln logs a message at level Debug on the standard logger.
func (logger *Logger) Debugln(args ...interface{}) {
	logger.header(1).Debugln(args...)
}

// Println logs a message at level Info on the standard logger.
func (logger *Logger) Println(args ...interface{}) {
	logger.header(1).Println(args...)
}

// Infoln logs a message at level Info on the standard logger.
func (logger *Logger) Infoln(args ...interface{}) {
	logger.header(1).Infoln(args...)
}

// Warnln logs a message at level Warn on the standard logger.
func (logger *Logger) Warnln(args ...interface{}) {
	logger.header(1).Warnln(args...)
}

// Warningln logs a message at level Warn on the standard logger.
func (logger *Logger) Warningln(args ...interface{}) {
	logger.header(1).Warningln(args...)
}

// Errorln logs a message at level Error on the standard logger.
func (logger *Logger) Errorln(args ...interface{}) {
	logger.header(1).Errorln(args...)
}

// Panicln logs a message at level Panic on the standard logger.
func (logger *Logger) Panicln(args ...interface{}) {
	logger.header(1).Panicln(args...)
}

// Fatalln logs a message at level Fatal on the standard logger.
func (logger *Logger) Fatalln(args ...interface{}) {
	logger.header(1).Fatalln(args...)
}

func (logger *Logger) curGoroutineID() uint64 {
	goroutineSpace := []byte("goroutine ")
	bp := logger.littleBuf.Get().(*[]byte)
	defer logger.littleBuf.Put(bp)
	b := *bp
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, goroutineSpace)
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		// panic(fmt.Sprintf("No space found in %q", b))
		return 0
	}
	b = b[:i]
	n, err := logger.parseUintBytes(b, 10, 64)
	if err != nil {
		// panic(fmt.Sprintf("Failed to parse goroutine ID out of %q: %v", b, err))
		return 0
	}
	return n
}

// parseUintBytes is like strconv.ParseUint, but using a []byte.
func (logger *Logger) parseUintBytes(s []byte, base int, bitSize int) (n uint64, err error) {
	var cutoff, maxVal uint64

	if bitSize == 0 {
		bitSize = int(strconv.IntSize)
	}

	s0 := s
	switch {
	case len(s) < 1:
		err = strconv.ErrSyntax
		goto Error

	case 2 <= base && base <= 36:
		// valid base; nothing to do

	case base == 0:
		// Look for octal, hex prefix.
		switch {
		case s[0] == '0' && len(s) > 1 && (s[1] == 'x' || s[1] == 'X'):
			base = 16
			s = s[2:]
			if len(s) < 1 {
				err = strconv.ErrSyntax
				goto Error
			}
		case s[0] == '0':
			base = 8
		default:
			base = 10
		}

	default:
		err = errors.New("invalid base " + strconv.Itoa(base))
		goto Error
	}

	n = 0
	cutoff = logger.cutoff64(base)
	maxVal = 1<<uint(bitSize) - 1

	for i := 0; i < len(s); i++ {
		var v byte
		d := s[i]
		switch {
		case '0' <= d && d <= '9':
			v = d - '0'
		case 'a' <= d && d <= 'z':
			v = d - 'a' + 10
		case 'A' <= d && d <= 'Z':
			v = d - 'A' + 10
		default:
			n = 0
			err = strconv.ErrSyntax
			goto Error
		}
		if int(v) >= base {
			n = 0
			err = strconv.ErrSyntax
			goto Error
		}

		if n >= cutoff {
			// n*base overflows
			n = 1<<64 - 1
			err = strconv.ErrRange
			goto Error
		}
		n *= uint64(base)

		n1 := n + uint64(v)
		if n1 < n || n1 > maxVal {
			// n+v overflows
			n = 1<<64 - 1
			err = strconv.ErrRange
			goto Error
		}
		n = n1
	}

	return n, nil

Error:
	return n, &strconv.NumError{Func: "ParseUint", Num: string(s0), Err: err}
}

// Return the first number n such that n*base >= 1<<64.
func (logger *Logger) cutoff64(base int) uint64 {
	if base < 2 {
		return 0
	}
	return (1<<64-1)/uint64(base) + 1
}
