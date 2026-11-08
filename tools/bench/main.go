// Command bench 测量 convert-md-picture 的性能。
//
// 它是性能迭代用的尺子：造一批 Markdown 夹具（含各种图片写法、远程图、缺失图、
// 代码块里的图），起一个本地假图床，然后分别测
//
//	parse：纯解析（mdscan.Scan）的吞吐与分配 —— 不含 IO、不含网络
//	e2e  ：端到端跑一遍 conv.Runner（扫描 → 上传 → 回写），输出吞吐与内存峰值
//
// 用法示例：
//
//	go run ./tools/bench -bench both -files 200 -refs 20 -pool 50 -repeat 3
//	go run ./tools/bench -bench e2e -endpoint http://127.0.0.1:36677 -verify   # 打真实 PicGo
//	go run ./tools/bench -bench e2e -cpuprofile cpu.prof -memprofile mem.prof
//
// 数字是给自己做前后对比用的，所以默认会多跑几轮取最好值（-repeat）。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/geekMus/convert-md-picture/internal/conv"
	"github.com/geekMus/convert-md-picture/internal/i18n"
	"github.com/geekMus/convert-md-picture/internal/imgpath"
	"github.com/geekMus/convert-md-picture/internal/mdscan"
	"github.com/geekMus/convert-md-picture/internal/settings"
	"github.com/geekMus/convert-md-picture/internal/uploadcache"
)

var (
	mode       = flag.String("bench", "both", "测什么：parse / e2e / both")
	files      = flag.Int("files", 200, "夹具文档数")
	refs       = flag.Int("refs", 20, "每篇文档里的本地图片引用数")
	pool       = flag.Int("pool", 50, "图片池大小（多篇文档共用同一批图，用来测去重）")
	imgKB      = flag.Int("kb", 8, "每张图片大约多少 KB")
	proseKB    = flag.Int("prose", 6, "每篇文档里正文文字大约多少 KB（让文档大小接近真实写作）")
	concur     = flag.Int("concurrency", 4, "并发数（对应界面里的「并发数」）")
	repeat     = flag.Int("repeat", 3, "重复轮数，取最好的一次")
	dirFlag    = flag.String("dir", "", "夹具目录（默认新建临时目录并用完删除）")
	keep       = flag.Bool("keep", false, "保留夹具目录")
	endpoint   = flag.String("endpoint", "", "图床地址；留空则用进程内的假图床")
	verify     = flag.Bool("verify", false, "跑完检查输出文件里的替换结果")
	dryRun     = flag.Bool("dryrun", false, "只扫描与分类，不上传也不回写（用来把各阶段耗时拆开）")
	reuse      = flag.String("reuse", "", "上传结果缓存的路径；给了就挂上缓存（同一进程里第 2 轮起就能命中）")
	reuseFix   = flag.Bool("reusefixture", false, "夹具目录里已有的文件不重写（保住图片时间戳，用来测跨进程的缓存命中）")
	fresh      = flag.Bool("fresh", false, "每轮开始前删掉上次的输出文件（模拟首次写入，而不是覆盖已有文件）")
	cpuProfile = flag.String("cpuprofile", "", "把 e2e 的 CPU profile 写到这个文件")
	memProfile = flag.String("memprofile", "", "把 e2e 的堆 profile 写到这个文件")
	quiet      = flag.Bool("quiet", false, "只打印最后一行汇总")
)

// tinyPNG 是 1x1 的占位图，-kb 0 时用它。
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// proseBlock 是正文填充用的段落（含中文，让字节数贴近真实中文文档）。
const proseBlock = "这一段是填充正文。扫描器要跳过代码块、行内代码和 HTML 注释，" +
	"还要认出各式各样的图片写法，所以文档里既有说明文字也有引用。\n\n"

type fixture struct {
	dir    string
	files  []string
	refs   int // 每篇文档的本地引用数
	total  int // 夹具目录里所有 .md 的字节数
	docs   [][]byte
	images int // 图片池大小
}

// writeFixture 写一个夹具文件。-reusefixture 时已存在的文件原样保留：
// 图片的修改时间不变，跨运行的上传缓存才有机会命中。
func writeFixture(path string, data []byte) error {
	if *reuseFix {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// buildFixtures 造夹具：一个图片池 + N 篇引用它们的 Markdown。
func buildFixtures(dir string, n, refsPerFile, poolSize, kb int) (*fixture, error) {
	imgDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return nil, err
	}

	// 图片：-kb 0 用 1x1，否则造一张随机像素图（随机数据的 PNG 压不动，
	// 体积大概就是 4 字节/像素，方便控制大小）。
	var img []byte
	if kb <= 0 {
		img = tinyPNG
	} else {
		side := 16
		for side*side*4 < kb*1024 {
			side *= 2
		}
		var err error
		img, err = randomPNG(side)
		if err != nil {
			return nil, err
		}
	}
	names := make([]string, poolSize)
	for i := range names {
		names[i] = fmt.Sprintf("img-%03d.png", i)
		rel := filepath.Join("images", names[i])
		if err := writeFixture(filepath.Join(dir, rel), img); err != nil {
			return nil, err
		}
	}

	// 缺一张图，用来验证「找不到文件」的路径不拖慢整体。
	if err := os.Remove(filepath.Join(dir, "images", names[len(names)-1])); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	f := &fixture{dir: dir, refs: refsPerFile, images: poolSize}
	rnd := rand.New(rand.NewSource(20261109))
	for i := 0; i < n; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "# 文档 %d\n\n正文段落，用来说明这个夹具的形状。\n\n", i)
		for r := 0; r < refsPerFile; r++ {
			name := names[rnd.Intn(len(names))]
			switch r % 5 {
			case 0:
				fmt.Fprintf(&b, "![图 %d](images/%s \"标题 %d\")\n\n", r, name, r)
			case 1:
				fmt.Fprintf(&b, "行内引用 ![图 %d](images/%s) 后面还有文字。\n\n", r, name)
			case 2:
				fmt.Fprintf(&b, "<img src=\"images/%s\" alt=\"图 %d\" width=\"60\" />\n\n", name, r)
			case 3:
				// 反斜杠写法：Windows 上同样要能解析成相对路径。
				fmt.Fprintf(&b, "![反斜杠 %d](images\\%s)\n\n", r, name)
			default:
				fmt.Fprintf(&b, "![重复 %d](images/%s)\n\n", r, name)
			}
		}
		// 不该被处理的东西：远程图、行内代码、围栏代码块。
		b.WriteString("![远程图](https://example.com/remote.png)\n\n")
		b.WriteString("行内代码 `![代码里的图](images/img-000.png)` 不算。\n\n")
		b.WriteString("```markdown\n![代码块里的图](images/img-001.png)\n```\n")
		b.WriteString("\n![缺失的图](images/missing.png)\n")

		// 正文填充：让文档大小接近真实写作，解析口径才有意义。
		if *proseKB > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Repeat(proseBlock, (*proseKB*1024)/len(proseBlock)+1))
		}

		data := []byte(b.String())
		name := fmt.Sprintf("doc-%04d.md", i)
		path := filepath.Join(dir, name)
		if err := writeFixture(path, data); err != nil {
			return nil, err
		}
		f.files = append(f.files, path)
		f.docs = append(f.docs, data)
		f.total += len(data)
	}
	return f, nil
}

func randomPNG(side int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	rnd := rand.New(rand.NewSource(7))
	buf := make([]byte, side*side*4)
	if _, err := rnd.Read(buf); err != nil {
		return nil, err
	}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			o := (y*side + x) * 4
			img.Set(x, y, color.RGBA{buf[o], buf[o+1], buf[o+2], 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// mockPicgo 起一个假图床，返回它的地址和已收到的上传次数。
func mockPicgo() (*httptest.Server, *int64) {
	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			List []string `json:"list"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		atomic.AddInt64(&count, int64(len(req.List)))
		result := make([]string, 0, len(req.List))
		for _, p := range req.List {
			result = append(result, "https://mock.local/"+filepath.Base(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  result,
		})
	}))
	return srv, &count
}

type e2eResult struct {
	wall      time.Duration
	summary   conv.Summary
	uploads   int64
	events    int64
	peakHeap  uint64
	totalHeap uint64
	perFile   []time.Duration
}

// runE2E 跑一遍端到端：扫描 → 上传 → 回写，统计耗时、事件数与内存峰值。
func runE2E(ctx context.Context, cfg settings.Settings, f *fixture) (e2eResult, error) {
	var events int64
	var active int64
	var maxActive int64

	emit := func(ev conv.Event) {
		atomic.AddInt64(&events, 1)
		if ev.Type == conv.EventFileStarted {
			n := atomic.AddInt64(&active, 1)
			for {
				old := atomic.LoadInt64(&maxActive)
				if n <= old || atomic.CompareAndSwapInt64(&maxActive, old, n) {
					break
				}
			}
		}
		if ev.Type == conv.EventFileFinished {
			atomic.AddInt64(&active, -1)
		}
	}

	// 采样内存峰值。
	stop := make(chan struct{})
	var peak uint64
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > atomic.LoadUint64(&peak) {
				atomic.StoreUint64(&peak, ms.HeapInuse)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	runner := conv.NewRunner(cfg, i18n.ZH, "bench", emit)
	if *reuse != "" {
		runner.UseUploadCache(uploadcache.Open(*reuse))
	}
	results, sum := runner.RunCollect(ctx, f.files)
	wall := time.Since(start)
	runtime.ReadMemStats(&after)
	close(stop)

	res := e2eResult{
		wall:      wall,
		summary:   sum,
		events:    events,
		peakHeap:  atomic.LoadUint64(&peak),
		totalHeap: after.TotalAlloc - before.TotalAlloc,
		perFile:   make([]time.Duration, len(results)),
	}
	return res, nil
}

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(runtime.NumCPU())

	dir := *dirFlag
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "cmp-bench-")
		if err != nil {
			fail(err)
		}
		if !*keep {
			defer os.RemoveAll(dir)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}

	if !*quiet {
		fmt.Printf("# 夹具目录 %s\n", dir)
	}
	f, err := buildFixtures(dir, *files, *refs, *pool, *imgKB)
	if err != nil {
		fail(err)
	}
	if !*quiet {
		fmt.Printf("# 夹具：%d 篇文档 · 每篇 %d 处本地引用 · 图片池 %d 张（最后一张故意删掉）· 文档合计 %.1f MB\n",
			len(f.files), f.refs, f.images, float64(f.total)/(1<<20))
	}

	if *mode == "parse" || *mode == "both" {
		runParse(f)
	}
	if *mode == "e2e" || *mode == "both" {
		runE2EAndReport(dir, f)
	}
}

func runParse(f *fixture) {
	// 纯解析：把文档预读进内存，只量 mdscan 本身的吞吐与分配。
	//
	// profile 要在这里自己起：-cpuprofile 原来只挂在 e2e 路径上，于是
	// `-bench parse -cpuprofile x` 什么文件都不会生成（踩过一次，白等一轮）。
	var cpuFile *os.File
	if *cpuProfile != "" {
		f2, err := os.Create(*cpuProfile)
		if err != nil {
			fail(err)
		}
		defer f2.Close()
		cpuFile = f2
		if err := pprof.StartCPUProfile(cpuFile); err != nil {
			fail(err)
		}
		defer pprof.StopCPUProfile()
	}
	bench := testing.Benchmark(func(b *testing.B) {
		var refs int
		for i := 0; i < b.N; i++ {
			for _, src := range f.docs {
				doc := mdscan.Scan(src)
				refs += len(doc.Refs)
			}
		}
		_ = refs
	})
	perOp := bench.NsPerOp()
	mbPerOp := float64(f.total) / (1 << 20)
	mbps := 0.0
	if perOp > 0 {
		mbps = mbPerOp / (float64(perOp) / 1e9)
	}
	if !*quiet {
		fmt.Printf("# parse：%.1f MB 语料\n", mbPerOp)
	}
	fmt.Printf("[bench] parse  files=%d docs=%.1fMB ns/op=%d MB/s=%.0f allocs/op=%d bytes/op=%d\n",
		len(f.files), mbPerOp, perOp, mbps, bench.AllocsPerOp(), bench.AllocedBytesPerOp())
}

func runE2EAndReport(dir string, f *fixture) {
	cfg := settings.Default()
	cfg.Output = settings.OutputSidecar
	cfg.Concurrency = *concur
	cfg.Retries = 1
	cfg.Backup = false
	cfg.Suffix = ".converted"
	cfg.DryRun = *dryRun

	var srv *httptest.Server
	var uploads *int64
	if *endpoint == "" {
		srv, uploads = mockPicgo()
		defer srv.Close()
		cfg.Endpoint = srv.URL
	} else {
		cfg.Endpoint = *endpoint
		uploads = new(int64) // 真实 PicGo，数不了，只做功能验证
	}
	if err := cfg.Normalize(); err != nil {
		fail(err)
	}

	var cpuFile, memFile *os.File
	if *cpuProfile != "" {
		f2, err := os.Create(*cpuProfile)
		if err != nil {
			fail(err)
		}
		defer f2.Close()
		cpuFile = f2
		if err := pprof.StartCPUProfile(cpuFile); err != nil {
			fail(err)
		}
		defer pprof.StopCPUProfile()
	}

	var best e2eResult
	var lastErr error
	walls := make([]time.Duration, 0, *repeat)
	for i := 0; i < *repeat; i++ {
		if *fresh {
			dropOutputs(f)
		}
		before := atomic.LoadInt64(uploads)
		res, err := runE2E(context.Background(), cfg, f)
		if err != nil {
			lastErr = err
			break
		}
		res.uploads = atomic.LoadInt64(uploads) - before
		walls = append(walls, res.wall)
		if !*quiet {
			fmt.Printf("[bench] 第 %d/%d 轮 wall=%s uploads=%d reused=%d\n",
				i+1, *repeat, res.wall.Round(time.Millisecond), res.uploads, res.summary.Reused)
		}
		if best.wall == 0 || res.wall < best.wall {
			best = res
		}
	}
	if lastErr != nil {
		fail(lastErr)
	}

	// 这台机器上有杀毒软件的实时防护，首次创建文件的耗时会飘，
	// 所以把每一轮的墙钟时间都列出来，别只看最好的一次。
	sorted := append([]time.Duration(nil), walls...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	median := sorted[len(sorted)/2]
	spread := ""
	if len(sorted) > 1 {
		spread = fmt.Sprintf("%s..%s 中位%s", sorted[0].Round(time.Millisecond), sorted[len(sorted)-1].Round(time.Millisecond), median.Round(time.Millisecond))
	} else {
		spread = sorted[0].Round(time.Millisecond).String()
	}

	if memFile == nil && *memProfile != "" {
		f2, err := os.Create(*memProfile)
		if err != nil {
			fail(err)
		}
		memFile = f2
		defer f2.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(memFile); err != nil {
			fail(err)
		}
	}

	filesPerSec := float64(best.summary.Files) / best.wall.Seconds()
	imagesPerSec := float64(best.summary.Images) / best.wall.Seconds()
	label := "e2e"
	if *dryRun {
		label = "e2e(dry)"
	}
	fmt.Printf("[bench] %-8s files=%d images=%d uploaded=%d reused=%d failedImages=%d wall=%s files/s=%.1f images/s=%.0f uploads=%d events=%d peakHeap=%.1fMB totalAlloc=%.1fMB\n",
		label,
		best.summary.Files, best.summary.Images, best.summary.Uploaded, best.summary.Reused, best.summary.FailedImages,
		best.wall.Round(time.Millisecond), filesPerSec, imagesPerSec,
		best.uploads, best.events,
		float64(best.peakHeap)/(1<<20), float64(best.totalHeap)/(1<<20))
	fmt.Printf("[bench] %-8s 各轮墙钟：%s\n", label, spread)

	if *verify {
		verifyOutputs(f, best.summary)
	}
}

// verifyOutputs 检查回写结果。判据不看具体图床地址（真实 PicGo 的地址各不相同）：
// 输出里不该再剩下任何「指向真实存在的本地图片」的引用；代码块、行内代码、
// 远程图、本来就缺失的图必须原样保留。
func verifyOutputs(f *fixture, sum conv.Summary) {
	var filesOK, leftLocal int
	var keptRemote, keptCode, missingKept int
	for _, src := range f.files {
		out := strings.TrimSuffix(src, ".md") + ".converted.md"
		data, err := os.ReadFile(out)
		if err != nil {
			fail(err)
		}
		text := string(data)
		if strings.Contains(text, "https://example.com/remote.png") {
			keptRemote++
		}
		if strings.Contains(text, "![代码块里的图](images/img-001.png)") {
			keptCode++
		}
		if strings.Contains(text, "![缺失的图](images/missing.png)") {
			missingKept++
		}

		bad := 0
		for _, ref := range mdscan.Scan(data).Refs {
			if imgpath.Classify(ref.Target) != imgpath.Local {
				continue
			}
			abs, ok := imgpath.Resolve(out, ref.Target)
			if !ok {
				continue
			}
			if st, serr := os.Stat(abs); serr == nil && !st.IsDir() {
				bad++ // 图还在、引用没被换掉 → 漏了
			}
		}
		leftLocal += bad
		if bad == 0 {
			filesOK++
		}
	}

	fmt.Printf("[verify] 输出文件=%d 已全部替换=%d/%d 残留本地引用=%d 远程保留=%d/%d 代码块保留=%d/%d 缺失保留=%d/%d 汇总上传=%d\n",
		len(f.files), filesOK, len(f.files), leftLocal,
		keptRemote, len(f.files), keptCode, len(f.files),
		missingKept, len(f.files), sum.Uploaded)
	if filesOK != len(f.files) {
		fail(fmt.Errorf("有 %d 篇文档还残留本地图片引用", len(f.files)-filesOK))
	}
}

// dropOutputs 删掉上一轮生成的输出文件，让下一轮量的是「首次写入」而不是覆盖。
func dropOutputs(f *fixture) {
	for _, src := range f.files {
		_ = os.Remove(strings.TrimSuffix(src, ".md") + ".converted.md")
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "bench: %v\n", err)
	os.Exit(1)
}
