package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
)

//go:embed all:web
var webFS embed.FS

func main() {
	initDPI()

	engine := NewReceiverEngine()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 根路径返回主界面；/js/ 等静态资源交给子 FS
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("无法解包 web 资源: %v", err)
	}
	http.Handle("/js/", http.StripPrefix("/js/", http.FileServer(http.FS(sub))))

	http.HandleFunc("/api/windows", func(w http.ResponseWriter, r *http.Request) {
		wins, err := ListAllWindows()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wins)
	})

	http.HandleFunc("/api/start", func(w http.ResponseWriter, r *http.Request) {
		hStr := r.URL.Query().Get("handle")
		var handle uint64
		if hStr != "" {
			handle, _ = strconv.ParseUint(hStr, 10, 64)
		}
		engine.StartCapture(handle)
		w.Write([]byte(`{"status":"ok"}`))
	})

	http.HandleFunc("/api/stop", func(w http.ResponseWriter, r *http.Request) {
		engine.StopCapture()
		w.Write([]byte(`{"status":"ok"}`))
	})

	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		st := engine.GetStatus()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	})

	// 摄像头/手机扫码模式：前端 (jsQR) 解码出文本后上报到这里，
	// 与屏幕捕获共用同一套标准/喷泉协议处理，完成组包与落盘。
	http.HandleFunc("/api/decode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
			http.Error(w, "bad request", 400)
			return
		}
		engine.handleDecoded(req.Text)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	http.HandleFunc("/api/preview.jpg", func(w http.ResponseWriter, r *http.Request) {
		data := engine.GetPreview()
		if len(data) == 0 {
			http.Error(w, "no preview available", 404)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(data)
	})

	// 动态绑定可用本地端口
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	urlStr := fmt.Sprintf("http://127.0.0.1:%d", port)

	fmt.Printf("\n======================================================\n")
	fmt.Printf("📡 AirScan-QR Windows 专用高能接收端已启动！\n")
	fmt.Printf("🌐 本地服务地址: %s\n", urlStr)
	fmt.Printf("👉 正在自动为您打开界面，如未打开请手动访问上方地址。\n")
	fmt.Printf("======================================================\n\n")

	go openBrowser(urlStr)

	if err := http.Serve(listener, nil); err != nil {
		log.Fatal(err)
	}
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	}
}
