package tkhttp

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

type requestLog struct {
	acc           adapter.LogAcc
	start         time.Time
	phase         string
	method        string
	url           string
	requestID     string
	requestBytes  int
	responseBytes int
	biz           map[string]any
}

func (s *requestLog) result(err error) {
	if err == nil {
		s.phase = PhaseComplete
		return
	}
	failure, ok := errors.AsType[*RequestError](err)
	if ok {
		s.phase = failure.Phase
		return
	}
	if errors.Is(err, ErrHTTPStatus) {
		s.phase = PhaseStatus
	}
}

func (s *requestLog) finish(ctx context.Context, response **Response, result *error) {
	if s.acc == nil {
		return
	}
	status := 0
	responseRequestID := ""
	if *response != nil {
		status = (*response).StatusCode
		responseRequestID = (*response).Header.Get(headerRequestID)
	}
	fields := map[string]any{
		"method":        s.method,
		"url":           s.url,
		"statusCode":    status,
		"requestBytes":  s.requestBytes,
		"responseBytes": s.responseBytes,
		"cost":          time.Since(s.start).String(),
		"phase":         s.phase,
		"errorKind":     errorKind(*result, s.phase),
	}
	if s.requestID != "" {
		fields["requestRequestId"] = s.requestID
	}
	if responseRequestID != "" {
		fields["responseRequestId"] = responseRequestID
	}
	s.acc.Update(map[string]any{"http": fields, "biz": s.biz})
	if *result == nil {
		s.acc.Info(ctx, "HTTP 请求完成")
		return
	}
	statusError := s.phase == PhaseStatus && errors.Is(*result, ErrHTTPStatus)
	if errors.Is(*result, context.Canceled) || statusError && status < 500 {
		s.acc.Warn(ctx, "HTTP 请求失败")
		return
	}
	s.acc.Error(ctx, "HTTP 请求失败")
}

func errorKind(err error, phase string) (kind string) {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrMiddleware):
		return "middleware"
	case errors.Is(err, ErrResponseTooLarge):
		return "responseTooLarge"
	case errors.Is(err, ErrRequestHook):
		return "requestHook"
	case errors.Is(err, ErrInvalidRequest):
		return "invalidRequest"
	case errors.Is(err, ErrRequestEncode):
		return "requestEncode"
	}
	if errors.Is(err, ErrHTTPStatus) {
		return "httpStatus"
	}
	return phase
}

// logVisit 仅用于复制期间识别共享映射和切片，保留循环引用以交由日志实现诊断。
type logVisit struct {
	typeOf reflect.Type
	ptr    uintptr
	length int
}

func cloneLogFields(fields map[string]any) (clone map[string]any) {
	if fields == nil {
		return nil
	}
	return cloneLogValue(reflect.ValueOf(fields), make(map[logVisit]reflect.Value)).Interface().(map[string]any)
}

// cloneLogValue 复制嵌套容器，指针、结构体及其内部引用仍由调用方保证同步。
func cloneLogValue(value reflect.Value, seen map[logVisit]reflect.Value) (clone reflect.Value) {
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return value
		}
		clone = reflect.New(value.Type()).Elem()
		clone.Set(cloneLogValue(value.Elem(), seen))
		return
	case reflect.Map, reflect.Slice:
		if value.IsNil() {
			return value
		}
		visit := logVisit{typeOf: value.Type(), ptr: uintptr(value.UnsafePointer()), length: value.Len()}
		if previous, exists := seen[visit]; exists {
			return previous
		}
		if value.Kind() == reflect.Map {
			clone = reflect.MakeMapWithSize(value.Type(), value.Len())
			seen[visit] = clone
			iterator := value.MapRange()
			for iterator.Next() {
				clone.SetMapIndex(iterator.Key(), cloneLogValue(iterator.Value(), seen))
			}
			return
		}
		clone = reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		seen[visit] = clone
		for i := range value.Len() {
			clone.Index(i).Set(cloneLogValue(value.Index(i), seen))
		}
		return
	case reflect.Array:
		clone = reflect.New(value.Type()).Elem()
		for i := range value.Len() {
			clone.Index(i).Set(cloneLogValue(value.Index(i), seen))
		}
		return
	}
	return value
}
