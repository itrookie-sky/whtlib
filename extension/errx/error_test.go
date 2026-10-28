package errx_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/errx"
	"github.com/itrookie-sky/whtlib/extension/logx"
	"github.com/itrookie-sky/whtlib/toolkit/tkhttp"
)

type branchError struct{ children []error }

func (e branchError) Error() (message string)    { return "branches" }
func (e branchError) Unwrap() (children []error) { return e.children }

type foreignError struct {
	cause  error
	code   string
	fields map[string]any
	frames []errx.Frame
}

func (e *foreignError) Error() (message string)           { return "foreign" }
func (e *foreignError) Unwrap() (cause error)             { return e.cause }
func (e *foreignError) ErrorCode() (code string)          { return e.code }
func (e *foreignError) Fields() (fields map[string]any)   { return e.fields }
func (e *foreignError) StackTrace() (frames []errx.Frame) { return e.frames }

// recordingLogger 只用于验证 LogAcc.Error 的借用视图及正文序列化路径。
type recordingLogger struct {
	adapter.Logger
	logger *slog.Logger
}

func (l *recordingLogger) Skip(n int) (logger adapter.Logger) { return l }
func (l *recordingLogger) Error(ctx context.Context, message string, args ...any) {
	if message != "" {
		args = append([]any{message}, args...)
	}
	l.logger.ErrorContext(ctx, strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

func TestPreserveErrorChain(t *testing.T) {
	status := &tkhttp.StatusError{StatusCode: 503}
	request := &tkhttp.RequestError{Phase: tkhttp.PhaseTransport, Err: errors.Join(tkhttp.ErrTransport, context.DeadlineExceeded, status)}
	wrapped := errx.WithStack(errx.WithFields(request, map[string]any{"operation": "fetch"}))
	if wrapped.Error() != request.Error() || strings.Contains(wrapped.Error(), "fetch") {
		t.Fatalf("错误文本被元数据改变: %s", wrapped)
	}
	for _, target := range []error{request, status, tkhttp.ErrTransport, tkhttp.ErrHTTPStatus, context.DeadlineExceeded} {
		if !errors.Is(wrapped, target) {
			t.Errorf("错误类别丢失: %v", target)
		}
	}
	gotRequest, ok := errors.AsType[*tkhttp.RequestError](wrapped)
	if !ok || gotRequest != request {
		t.Fatal("RequestError 类型丢失")
	}
	gotStatus, ok := errors.AsType[*tkhttp.StatusError](wrapped)
	if !ok || gotStatus.StatusCode != 503 {
		t.Fatal("StatusError 状态码丢失")
	}
}

func TestStackLocationAndReuse(t *testing.T) {
	base := errors.New("original")
	_, file, line, _ := runtime.Caller(0)
	wrapped := errx.WithStack(base)
	frames := errx.Stack(wrapped)
	if len(frames) == 0 || frames[0].File != file || frames[0].Line != line+1 || !strings.HasSuffix(frames[0].Function, ".TestStackLocationAndReuse") {
		t.Fatalf("首帧应指向调用处: %+v", frames)
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("堆栈包装丢失原始原因")
	}
	if errx.WithStack(wrapped) != wrapped {
		t.Fatal("重复采集堆栈")
	}
	joined := errors.Join(errors.New("left"), wrapped)
	if errx.WithStack(joined) != joined {
		t.Fatal("分支中已有堆栈时不应重复采集")
	}
	want := frames[0]
	frames[0].File = "modified"
	if errx.Stack(wrapped)[0] != want {
		t.Fatal("修改返回堆栈污染包装器")
	}
	if errx.WithStack(nil) != nil || errx.Stack(nil) != nil || errx.Stack(base) != nil {
		t.Fatal("nil 或无堆栈结果错误")
	}
}

func TestFieldsPriorityAndCopies(t *testing.T) {
	input := map[string]any{"operation": "inner", "left": 1, "nil": "inner"}
	left := errx.WithFields(errors.New("left"), input)
	input["operation"] = "changed"
	input["injected"] = true
	right := errx.WithFields(errors.New("right"), map[string]any{"left": 2, "right": 3})
	joined := errors.Join(left, right)
	outer := errx.WithFields(joined, map[string]any{"operation": "outer", "nil": nil})
	want := map[string]any{"operation": "outer", "left": 1, "right": 3, "nil": nil}
	got := errx.Fields(outer)
	if !maps.Equal(got, want) {
		t.Fatalf("字段优先级: %v", got)
	}
	got["operation"] = "mutated"
	delete(got, "left")
	if !maps.Equal(errx.Fields(outer), want) {
		t.Fatal("修改提取字段污染包装器")
	}
	if errx.Fields(left)["operation"] != "inner" {
		t.Fatal("输入 map 未复制")
	}
	if errx.WithFields(nil, input) != nil || errx.Fields(nil) != nil || errx.Fields(errors.New("plain")) != nil {
		t.Fatal("nil 处理不正确")
	}
	if errx.WithFields(left, nil) != left {
		t.Fatal("空字段不应增加包装")
	}
}

func TestWalkAndLeaves(t *testing.T) {
	a := errors.New("a")
	b := errors.New("b")
	wrapped := fmt.Errorf("wrapped: %w", a)
	inner := errors.Join(wrapped, b)
	root := errors.Join(inner, a)
	if got := slices.Collect(errx.Walk(root)); !slices.Equal(got, []error{root, inner, wrapped, a, b, a}) {
		t.Fatalf("遍历顺序: %v", got)
	}
	if got := slices.Collect(errx.Leaves(root)); !slices.Equal(got, []error{a, b, a}) {
		t.Fatalf("叶子顺序: %v", got)
	}
	for range errx.Walk(nil) {
		t.Fatal("nil 应没有节点")
	}
	for range errx.Leaves(nil) {
		t.Fatal("nil 应没有叶子")
	}
	count := 0
	for range errx.Walk(root) {
		count++
		break
	}
	if count != 1 {
		t.Fatal("提前终止未生效")
	}
	for range errx.Leaves(root) {
		break
	}
	// 非可比较的错误值和 nil 子节点也能遍历，不依赖 map[error] 去重。
	branch := branchError{children: []error{nil, a, nil, b}}
	if got := slices.Collect(errx.Leaves(branch)); !slices.Equal(got, []error{a, b}) {
		t.Fatalf("nil 子节点: %v", got)
	}
	if got := slices.Collect(errx.Leaves(branchError{})); len(got) != 1 {
		t.Fatalf("空分支应为叶子: %v", got)
	}
}

func TestCodeError(t *testing.T) {
	base := errors.New("private cause")
	coded := &errx.CodeError{Code: "ORDER_NOT_FOUND", Message: "订单不存在", Err: base}
	wrapped := errx.WithFields(fmt.Errorf("service: %w", coded), map[string]any{"orderId": 123})
	if errx.CodeOf(wrapped) != coded.Code || coded.Error() != coded.Message || !errors.Is(wrapped, base) {
		t.Fatalf("业务错误: %s", wrapped)
	}
	got, ok := errors.AsType[*errx.CodeError](wrapped)
	if !ok || got != coded {
		t.Fatal("业务错误类型丢失")
	}
	if errors.Is(wrapped, &errx.CodeError{Code: coded.Code}) {
		t.Fatal("不应仅因错误码相同而等价")
	}
	outer := &errx.CodeError{Code: "SERVICE_FAILED", Err: coded}
	if errx.CodeOf(outer) != "SERVICE_FAILED" {
		t.Fatal("外层错误码应优先")
	}
	if errx.CodeOf(&errx.CodeError{Err: coded}) != coded.Code {
		t.Fatal("空错误码应继续查找")
	}
	if errx.CodeOf(errors.Join(base, coded)) != coded.Code {
		t.Fatal("未查找分支错误码")
	}
	if errx.CodeOf(nil) != "" || errx.CodeOf(base) != "" {
		t.Fatal("缺失错误码不正确")
	}
	if (&errx.CodeError{Code: "CODE"}).Error() != "CODE" || (&errx.CodeError{Err: base}).Error() != base.Error() || (&errx.CodeError{}).Error() == "" {
		t.Fatal("错误文本回退不正确")
	}
	var nilCode *errx.CodeError
	if nilCode.Error() != "" || nilCode.Unwrap() != nil || nilCode.ErrorCode() != "" {
		t.Fatal("nil CodeError 处理不正确")
	}
}

func TestForeignMetadata(t *testing.T) {
	wantFrame := errx.Frame{Function: "foreign.call", File: "foreign.go", Line: 12}
	foreign := &foreignError{code: "FOREIGN", fields: map[string]any{"key": "value"}, frames: []errx.Frame{wantFrame}}
	if errx.WithStack(foreign) != foreign || errx.CodeOf(foreign) != "FOREIGN" {
		t.Fatal("外部元数据未识别")
	}
	frames := errx.Stack(foreign)
	fields := errx.Fields(foreign)
	frames[0].Line = 1
	fields["key"] = "changed"
	if foreign.frames[0] != wantFrame || foreign.fields["key"] != "value" {
		t.Fatal("提取结果与外部容器共享")
	}
	if len(errx.Stack(errx.WithStack(&foreignError{}))) == 0 {
		t.Fatal("空外部堆栈应允许采集")
	}
}

func TestDescribeTreeAndSnapshots(t *testing.T) {
	left := errx.WithFields(&errx.CodeError{Code: "LEFT", Message: "left"}, map[string]any{"side": "left"})
	right := errx.WithStack(&errx.CodeError{Code: "RIGHT", Message: "right"})
	root := errx.WithFields(errors.Join(left, right), map[string]any{"operation": "batch"})
	d := errx.Describe(root)
	if d.Message != root.Error() || d.Code != "LEFT" || !maps.Equal(d.Fields, errx.Fields(root)) || !slices.Equal(d.Stack, errx.Stack(root)) {
		t.Fatalf("诊断汇总: %+v", d)
	}
	parents := make([]int, 0, len(d.Nodes))
	for _, node := range d.Nodes {
		parents = append(parents, node.Parent)
	}
	if !slices.Equal(parents, []int{-1, 0, 1, 2, 1, 4}) {
		t.Fatalf("父子关系: %v", parents)
	}
	if d.Nodes[3].Code != "LEFT" || d.Nodes[5].Code != "RIGHT" || d.Nodes[0].Type == "" {
		t.Fatalf("节点元数据: %+v", d.Nodes)
	}
	d.Fields["operation"] = "changed"
	d.Nodes[0].Fields["operation"] = "changed again"
	d.Stack[0].Line = 0
	d.Nodes[4].Stack[0].Line = 0
	if errx.Fields(root)["operation"] != "batch" || errx.Stack(root)[0].Line == 0 {
		t.Fatal("修改诊断快照污染原始错误")
	}
	if nilDescription := errx.Describe(nil); nilDescription.Message != "" || len(nilDescription.Nodes) != 0 {
		t.Fatal("nil 诊断结果错误")
	}
	encoded, err := json.Marshal(errx.Describe(root))
	if err != nil {
		t.Fatal(err)
	}
	var decoded errx.Description
	err = json.Unmarshal(encoded, &decoded)
	if err != nil || len(decoded.Nodes) != 6 || decoded.Code != "LEFT" {
		t.Fatalf("诊断 JSON: %s, %v", encoded, err)
	}
}

func TestDiagnosticLogging(t *testing.T) {
	base := &errx.CodeError{Code: "FAILED", Message: "operation failed"}
	d := errx.Describe(errx.WithStack(errx.WithFields(base, map[string]any{"operation": "fetch"})))
	var buffer bytes.Buffer
	slogger := slog.New(slog.NewJSONHandler(&buffer, nil))
	slogger.ErrorContext(t.Context(), "failed", "error", d)
	var record map[string]any
	err := json.Unmarshal(buffer.Bytes(), &record)
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := record["error"].(map[string]any)
	if !ok || detail["code"] != "FAILED" || detail["nodes"] == nil || detail["stack"] == nil {
		t.Fatalf("slog 诊断: %s", buffer.String())
	}
	buffer.Reset()
	acc := logx.NewLogAcc(logx.WithLogger(&recordingLogger{logger: slogger}))
	acc.SetValue("error", d)
	acc.Error(t.Context(), "failed")
	record = nil
	err = json.Unmarshal(buffer.Bytes(), &record)
	if err != nil {
		t.Fatalf("LogAcc 日志: %s, %v", buffer.String(), err)
	}
	message, ok := record["msg"].(string)
	if !ok {
		t.Fatalf("LogAcc 日志缺少正文: %s", buffer.String())
	}
	fieldsJSON, found := strings.CutPrefix(message, "failed ")
	if !found {
		t.Fatalf("LogAcc 正文: %s", message)
	}
	var fields map[string]errx.Description
	err = json.Unmarshal([]byte(fieldsJSON), &fields)
	if err != nil || fields["error"].Code != "FAILED" || len(fields["error"].Nodes) == 0 {
		t.Fatalf("LogAcc 诊断: %s, %v", fieldsJSON, err)
	}
	if empty := errx.Describe(nil).LogValue(); empty.Kind() != slog.KindGroup {
		t.Fatal("零值应可记录")
	}
}

func TestConcurrentSnapshots(t *testing.T) {
	base := errors.New("original")
	wrapped := errx.WithStack(errx.WithFields(base, map[string]any{"shared": "value"}))
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 10 {
				fields := errx.Fields(wrapped)
				fields["shared"] = "local"
				frames := errx.Stack(wrapped)
				frames[0].Line = 0
				d := errx.Describe(wrapped)
				d.Fields["shared"] = "local"
				if !errors.Is(wrapped, base) || errx.Fields(wrapped)["shared"] != "value" {
					t.Error("共享数据被污染")
				}
			}
		})
	}
	group.Wait()
}
