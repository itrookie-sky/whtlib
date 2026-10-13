package logx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

type outputState struct {
	mu       sync.Mutex
	writer   io.Writer
	owned    *FileWriter
	closed   bool
	close    sync.Once
	closeErr error
}

type boundAttrs struct {
	groups []string
	attrs  []slog.Attr
}

type handler struct {
	state   *outputState
	options Options
	groups  []string
	bound   []boundAttrs
}

func (h *handler) Enabled(_ context.Context, level slog.Level) (enabled bool) {
	return level >= h.options.Level.Level()
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
		if e, ok := value.(error); ok && !isNil(e) {
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
	contextData, err := contextJSON(ctx, h.options)
	var line strings.Builder
	if !record.Time.IsZero() {
		line.WriteString(record.Time.Local().Format("2006/01/02 15:04:05"))
		line.WriteByte(' ')
	}
	line.Write(contextData)
	if record.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{record.PC})
		frame, _ := frames.Next()
		if frame.File != "" {
			file := frame.File
			if !h.options.FullPath {
				file = filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file))
			}
			line.WriteByte(' ')
			line.WriteString(lineEscaper.Replace(file))
			line.WriteByte(':')
			line.WriteString(strconv.Itoa(frame.Line))
		}
	}
	line.WriteByte(' ')
	line.WriteString(record.Level.String())
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
