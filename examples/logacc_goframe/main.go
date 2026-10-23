package main

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/os/glog"
	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/logx"
	"github.com/itrookie-sky/whtlib/toolkit/tkhttp"
)

func main() {
	native := glog.New()
	native.SetFlags(glog.F_TIME_STD | glog.F_FILE_SHORT)
	native.SetStack(false)
	native.SetStdoutColorDisabled(true)
	backend := NewGoFrameLogger(native)
	acc := logx.NewLogAcc(logx.WithLogger(backend), logx.WithCost(time.Now()))
	acc.SetValue("orderId", "demo-001").Info(context.Background(), "订单处理完成")
}

// newHTTPClient 演示相同 GoFrame 后端供多个 HTTP 调用共享，累计器每次新建。
func newHTTPClient(baseURL string, backend adapter.Logger) (client *tkhttp.Client, err error) {
	return tkhttp.New(tkhttp.Config{BaseURL: baseURL}, tkhttp.WithLogAcc(func() (acc adapter.LogAcc) {
		return logx.NewLogAccAdapter(logx.WithLogger(backend))
	}))
}
