package tkhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/itrookie-sky/whtlib/toolkit/tkhttp"
)

func ExampleClient_Get() {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = fmt.Fprint(writer, `{"id":"device-1"}`)
	}))
	defer server.Close()
	client, err := tkhttp.New(tkhttp.Config{BaseURL: server.URL, DisableLog: true})
	if err != nil {
		panic(err)
	}
	defer client.CloseIdleConnections()
	result := struct {
		ID string `json:"id"`
	}{}
	response, err := client.Get(context.Background(), "/device", tkhttp.RequestConfig{Result: &result})
	if err != nil {
		panic(err)
	}
	fmt.Println(response.StatusCode, result.ID)
	// Output: 200 device-1
}
