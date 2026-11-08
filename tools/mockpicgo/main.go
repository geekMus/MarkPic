// Command mockpicgo 起一个假的 PicGo 本地服务，用来在「不真的传图」的前提下
// 压测图形界面：真图床每张图要几百毫秒，界面上千条事件的表现根本测不出来。
//
//	go run ./tools/mockpicgo -port 36678 -delay 5ms -fail 1/13
//
// -delay 是每次上传的模拟耗时；-fail 可以按比例注入失败（形如 1/13 = 每 13 张失败 1 张）。
// 它是开发工具，不进 exe 发布物。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
	port := flag.Int("port", 36678, "监听端口")
	delay := flag.Duration("delay", 5*time.Millisecond, "每次上传的模拟耗时")
	fail := flag.String("fail", "", "失败比例，形如 1/13；留空表示全部成功")
	work := flag.Duration("work", 0, "在返回前额外空转的时长（模拟 CPU 型图床）")
	quiet := flag.Bool("quiet", false, "不打印每次上传")
	flag.Parse()

	failEvery, failAt := 0, 0
	if *fail != "" {
		parts := strings.SplitN(*fail, "/", 2)
		if len(parts) != 2 {
			log.Fatalf("失败比例要写成 1/13 这样：%q", *fail)
		}
		a, err1 := strconv.Atoi(parts[0])
		b, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || b <= 0 || a < 0 {
			log.Fatalf("失败比例不合法：%q", *fail)
		}
		failEvery, failAt = b, a
	}

	var total, failed int64
	started := time.Now()

	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			List []string `json:"list"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil || len(req.List) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if *delay > 0 {
			time.Sleep(*delay)
		}
		if *work > 0 {
			until := time.Now().Add(*work)
			for time.Now().Before(until) {
			}
		}

		n := atomic.AddInt64(&total, int64(len(req.List)))
		result := make([]string, 0, len(req.List))
		ok := true
		for _, p := range req.List {
			if failEvery > 0 && n%int64(failEvery) < int64(failAt) {
				ok = false
				atomic.AddInt64(&failed, 1)
				break
			}
			result = append(result, "https://mock.local/"+filepath.Base(p))
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "[mock] #%d %s\n", n, filepath.Base(req.List[0]))
		}

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "模拟失败"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uploads":  atomic.LoadInt64(&total),
			"failed":   atomic.LoadInt64(&failed),
			"uptimeMs": time.Since(started).Milliseconds(),
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("[mock] 假图床在 http://%s（每张 %s，失败比例 %q）", addr, *delay, *fail)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
