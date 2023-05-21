package main

import (
	"context"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func loadEnv() {
	_ = godotenv.Load()
}

func passWebsocket(fromConn *websocket.Conn, toConn *websocket.Conn) {
	for {
		messageType, p, err := fromConn.ReadMessage()
		if err != nil {
			continue
		}

		err = toConn.WriteMessage(messageType, p)
		if err != nil {
			continue
		}
	}
}

func createClient() http.Client {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _ string, _ string) (net.Conn, error) {
				return net.Dial("unix", os.Getenv("CGI_PROXY_SOCKET_PATH"))
			},
		},
	}

	return client
}

func server() {
	_ = cgi.Serve(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			socketPath := os.Getenv("CGI_PROXY_SOCKET_PATH")

			if _, err := os.Stat(socketPath); err != nil {
				writer.WriteHeader(500)
				return
			}

			ip, _, _ := net.SplitHostPort(request.RemoteAddr)
			u, _ := url.Parse(request.URL.String())

			request.Header.Del("X-Forwarded-For")
			request.Header.Del("X-Forwarded-Host")
			request.Header.Del("X-Forwarded-Proto")

			request.Header.Add("X-Forwarded-For", ip)
			request.Header.Add("X-Forwarded-Host", u.Host)
			request.Header.Add("X-Forwarded-Proto", u.Scheme)

			requestPath := u.Path
			basePath, _ := filepath.Abs(os.Getenv("CGI_PROXY_BASE_PATH"))
			if basePath == "/" {
				basePath = ""
			}
			if len(basePath) != 0 && strings.HasPrefix(u.Path, basePath) {
				requestPath = requestPath[len(basePath):]
			}

			if strings.ToLower(request.Header.Get("Upgrade")) == "websocket" {
				u.Host = "unix"
				u.Scheme = "ws"
				u.Path = requestPath

				request.URL = u
				request.Host = u.Host

				originConn, originResponse, err := websocket.DefaultDialer.Dial(u.String(), request.Header)

				if err != nil {
					writer.WriteHeader(500)
					return
				}

				upgrader := websocket.Upgrader{}
				clientConn, err := upgrader.Upgrade(writer, request, originResponse.Header)

				if err != nil {
					writer.WriteHeader(500)
					return
				}

				defer func(originConn *websocket.Conn) {
					_ = originConn.Close()
				}(originConn)
				defer func(clientConn *websocket.Conn) {
					_ = clientConn.Close()
				}(clientConn)

				go passWebsocket(clientConn, originConn)
				go passWebsocket(originConn, clientConn)
			} else {
				u.Host = "unix"
				u.Scheme = "http"
				u.Path = requestPath

				request.URL = u
				request.Host = u.Host

				client := createClient()
				response, err := client.Do(request)

				if err != nil {
					writer.WriteHeader(500)
					return
				}

				writer.WriteHeader(response.StatusCode)
				for key, values := range response.Header {
					for _, value := range values {
						writer.Header().Add(key, value)
					}
				}

				body, _ := io.ReadAll(response.Body)
				_, _ = writer.Write(body)
			}
		}),
	)
}

func main() {
	loadEnv()
	server()
}
