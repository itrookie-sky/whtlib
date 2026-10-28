package logx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

type outputState struct {
	mu     sync.Mutex
	writer io.Writer
	lease  *fileLease
	closed bool
}

type boundAttrs struct {
	groups []string
	attrs  []slog.Attr
}

type handler struct {
	state  *outputState
	config Config
	groups []string
	bound  []boundAttrs
}

func (h *handler) Enabled(_ context.Context, level slog.Level) (enabled bool) {
	return level >= h.config.Level.Level()
}

func (h *handler) WithAttrs(attrs []slog.Attr) (result slog.Handler) {
	clone := *h
	clone.bound = append(slices.Clone(h.bound), boundAttrs{groups: slices.Clone(h.groups), attrs: slices.Clone(attrs)})
	return &clone
}

func (h *handler) WithGroup(name string) (result slog.Handler) {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(slices.Clone(h.groups), name)
	return &clone
}

func attributeFields(attrs []slog.Attr) (fields []jsonField, err error) {
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		if attr.Equal(slog.Attr{}) {
			continue
		}
		if attr.Value.Kind() == slog.KindGroup {
			children, childErr := attributeFields(attr.Value.Group())
			err = errors.Join(err, childErr)
			if len(children) == 0 {
				continue
			}
			if attr.Key == "" {
				fields = append(fields, children...)
				continue
			}
			fields = append(fields, jsonField{name: attr.Key, value: orderedObject(children)})
			continue
		}
		value := attr.Value.Any()
		if e, ok := value.(error); ok && !reflectx.IsNil(e) {
			value = e.Error()
		}
		fields = append(fields, jsonField{name: attr.Key, value: value})
	}
	return
}

func appendGrouped(fields []jsonField, groups []string, values []jsonField) (result []jsonField) {
	if len(values) == 0 {
		return fields
	}
	if len(groups) == 0 {
		return append(fields, values...)
	}
	for i := len(fields) - 1; i >= 0; i-- {
		children, ok := fields[i].value.(orderedObject)
		if fields[i].name == groups[0] && ok {
			fields[i].value = orderedObject(appendGrouped(children, groups[1:], values))
			return fields
		}
	}
	children := appendGrouped(nil, groups[1:], values)
	return append(fields, jsonField{name: groups[0], value: orderedObject(children)})
}

var lineEscaper = strings.NewReplacer("\r", `\r`, "\n", `\n`)

func (h *handler) Handle(ctx context.Context, record slog.Record) (err error) {
	contextData, err := contextJSON(ctx, h.config)
	traceData, traceErr := contextTrace(ctx, h.config)
	err = errors.Join(err, traceErr)
	var line strings.Builder
	if !record.Time.IsZero() {
		line.WriteString(record.Time.Local().String())
		line.WriteByte(' ')
	}
	lev := "[" + record.Level.String() + "]"
	line.WriteString(lev)
	if len(traceData) > 0 {
		line.WriteByte(' ')
		line.Write(traceData)
	}
	if len(contextData) > 0 {
		line.WriteByte(' ')
		line.Write(contextData)
	}
	if record.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{record.PC})
		frame, _ := frames.Next()
		if frame.File != "" {
			file := frame.File
			if !h.config.FullPath {
				file = filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file))
			}
			line.WriteByte(' ')
			line.WriteString(lineEscaper.Replace(file))
			line.WriteByte(':')
			line.WriteString(strconv.Itoa(frame.Line))
		}
	}
	line.WriteByte(' ')
	line.WriteString(lineEscaper.Replace(record.Message))
	var fields []jsonField
	for _, bound := range h.bound {
		part, partErr := attributeFields(bound.attrs)
		fields = appendGrouped(fields, bound.groups, part)
		err = errors.Join(err, partErr)
	}
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	part, partErr := attributeFields(attrs)
	fields = appendGrouped(fields, h.groups, part)
	err = errors.Join(err, partErr)
	if len(fields) > 0 {
		data, encodeErr := encodeFields(fields)
		err = errors.Join(err, encodeErr)
		line.WriteByte(' ')
		line.Write(data)
	}
	line.WriteByte('\n')
	data := []byte(line.String())
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.closed {
		return errors.Join(err, os.ErrClosed)
	}
	n, writeErr := h.state.writer.Write(data)
	if n != len(data) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(err, writeErr)
}

type jsonField struct {
	name  string
	value any
}

type orderedObject []jsonField

func encodeFields(fields []jsonField) (data []byte, err error) {
	data = append(data, '{')
	for i, field := range fields {
		if i > 0 {
			data = append(data, ',')
		}
		name, _ := json.Marshal(field.name)
		var value []byte
		var encodeErr error
		object, isObject := field.value.(orderedObject)
		if isObject {
			value, encodeErr = encodeFields(object)
		} else {
			value, encodeErr = json.Marshal(field.value)
		}
		if encodeErr != nil {
			err = errors.Join(err, fieldEncodingError(field.name, encodeErr))
			if !isObject {
				value = []byte(`"[json encode error]"`)
			}
		}
		data = append(data, name...)
		data = append(data, ':')
		data = append(data, value...)
	}
	data = append(data, '}')
	return
}

// appendContextField 读取上下文字段，忽略 nil 和空字符串。
func appendContextField(ctx context.Context, fields []jsonField, name string, key any) (result []jsonField) {
	value := ctx.Value(key)
	if reflectx.IsNil(value) {
		return fields
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.String && rv.Len() == 0 {
		return fields
	}
	return append(fields, jsonField{name: name, value: value})
}

func contextJSON(ctx context.Context, config Config) (data []byte, err error) {
	fields := make([]jsonField, 0, len(config.ContextFields))
	for _, field := range config.ContextFields {
		fields = appendContextField(ctx, fields, field.Name, field.Key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return encodeFields(fields)
}

// contextTrace 将 traceId 输出为独立占位，空值省略，字符串去掉外围引号并保留转义。
func contextTrace(ctx context.Context, config Config) (data []byte, err error) {
	fields := appendContextField(ctx, nil, "traceId", config.TraceKey)
	if len(fields) == 0 {
		return nil, nil
	}
	value, err := json.Marshal(fields[0].value)
	if err != nil {
		return []byte("{[json encode error]}"), traceEncodingError(err)
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	data = append(data, '{')
	data = append(data, value...)
	data = append(data, '}')
	return
}

// terminalWriter 串行输出完整日志记录，不拥有标准输出的生命周期。
type terminalWriter struct{ mu sync.Mutex }

var terminalOutput = &terminalWriter{}

func (w *terminalWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return os.Stdout.Write(p)
}

// dualWriter 始终尝试两个输出目标，一端失败不阻止另一端写入。
type dualWriter struct {
	terminal io.Writer
	file     io.Writer
}

func (w *dualWriter) Write(p []byte) (n int, err error) {
	terminalN, terminalErr := w.terminal.Write(p)
	if terminalN != len(p) && terminalErr == nil {
		terminalErr = io.ErrShortWrite
	}
	fileN, fileErr := w.file.Write(p)
	if fileN != len(p) && fileErr == nil {
		fileErr = io.ErrShortWrite
	}
	return min(terminalN, fileN), errors.Join(terminalErr, fileErr)
}
