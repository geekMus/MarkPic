package conv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/picgo"
)

// upload 上传一张图片，返回地址与这张图是怎么来的（uploaded/reused/cached）。
// 同一路径在一轮里只上传一次，其余复用结果；跨运行则靠上传结果缓存。
func (r *Runner) upload(ctx context.Context, absPath string) (string, string, error) {
	key := strings.ToLower(absPath)

	// 先认「这一轮已经在传/传过了」：同一张图在一轮里只该走到图床一次。
	// 这一步必须排在跨运行缓存前面，否则「同轮内第二个引用」会命中刚刚
	// 自己写进去的缓存条目，被记成 cached —— 那会把「本轮去重」和
	// 「跨运行复用」两件事混成一件，统计与界面都不再有意义。
	r.mu.Lock()
	entry, exists := r.cache[key]
	if !exists {
		entry = &cacheEntry{done: make(chan struct{})}
		r.cache[key] = entry
	}
	r.mu.Unlock()

	if exists {
		select {
		case <-entry.done:
			if entry.err != nil {
				// 领头那次没成：把失败原因如实带回去，取消引起的算取消。
				if ctx.Err() != nil {
					return "", StatusCancelled, entry.err
				}
				return "", StatusFailed, entry.err
			}
			return entry.url, StatusReused, nil
		case <-ctx.Done():
			return "", StatusCancelled, ctx.Err()
		}
	}

	// 这一轮第一次碰到这张图：再看跨运行的缓存——大小与修改时间都没变，
	// 说明上次已经传过同一张图，直接沿用当时的地址，这一轮不打图床。
	if r.uploads.Enabled() {
		if size, mtime, ok := r.fileStamp(absPath); ok {
			if url, hit := r.uploads.Lookup(r.client.Endpoint, key, size, mtime); hit {
				r.closeEntry(entry, url, nil)
				return url, StatusCached, nil
			}
		}
	}

	// 图床不会替我们看内容：0 字节的文件、导出到一半的图，传上去只会换来一个
	// 打不开的链接 —— 而文档里那条链接看上去已经「换好了」。
	//
	// looksComplete 只认几种常见格式的结尾标记（PNG 的 IEND、JPEG 的 FFD9…），
	// 认不出来的格式一律当完整：宁可放过去，也不能错杀用户那张奇怪格式的图。
	if unusableImage(absPath) {
		err := errors.New(i18n.T(r.lang, i18n.KeyConvFileIncomplete))
		r.closeEntry(entry, "", err)
		return "", StatusFailed, err
	}
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		r.closeEntry(entry, "", ctx.Err())
		return "", StatusCancelled, ctx.Err()
	}

	// 抢到并发额度之后再登记「这份内容有没有别人正在传」：大小 + sha256 一样的
	// 图只发一份。登记排在额度之后，是为了让跟随者不必也占着额度傻等。
	ce, leader, okContent := r.claimContent(absPath)
	if okContent && !leader {
		<-r.sem
		select {
		case <-ce.done:
			if ce.err == nil && ce.url != "" {
				r.closeEntry(entry, ce.url, nil)
				// 内容相同、路径不同：这一张没有产生上行流量，按「这一轮去重」记账。
				return ce.url, StatusReused, nil
			}
			// 领头那张没传成：把它的失败当成这一张的失败，不再重复敲一次门。
			r.closeEntry(entry, "", ce.err)
			if ctx.Err() != nil {
				return "", StatusCancelled, ce.err
			}
			// 跟随者也得记一次失败：它这一处同样没换成地址。只让领头记账的话，
			// 一批内容相同的图会把「连续失败」压成 1 次，图床整个挂掉时该提前
			// 收工的那条判断就永远不会触发（实测 20 张全失败却没有任何终止原因）。
			if picgo.IsRateLimited(ce.err) {
				r.noteRateLimited()
			} else {
				r.noteUploadFailure()
			}
			return "", StatusFailed, ce.err
		case <-ctx.Done():
			r.closeEntry(entry, "", ctx.Err())
			return "", StatusCancelled, ctx.Err()
		}
	}

	r.sent.Add(1)
	url, err := r.client.Upload(ctx, absPath)
	<-r.sem
	if okContent && leader {
		r.settleContent(ce, url, err)
	}
	r.closeEntry(entry, url, err)
	if err != nil {
		if ctx.Err() != nil {
			// 请求是被取消打断的，这一张没传成，但也不算「上传失败」。
			return "", StatusCancelled, err
		}
		// 只有「领头」的那个协程会走到这儿，失败计数不会被重复累加。
		if picgo.IsRateLimited(err) {
			r.noteRateLimited()
		} else {
			r.noteUploadFailure()
		}
		return "", StatusFailed, err
	}
	r.noteUploadSuccess()
	if size, mtime, ok := r.fileStamp(absPath); ok {
		r.uploads.Store(r.client.Endpoint, key, size, mtime, url)
	}
	return url, StatusUploaded, nil
}

func (r *Runner) closeEntry(e *cacheEntry, url string, err error) {
	e.url, e.err = url, err
	close(e.done)
}

// claimContent 在「同一份内容」的登记表上占位。leader 为真表示这一份由当前协程
// 负责上传；否则拿到的是别人的登记，等它 done 即可。
//
// ok 为假表示这张图太小、读不出来，或者它的大小还是第一次见（大小不同的图不可能
// 内容相同，连哈希都不用算）——那就照常自己传。
func (r *Runner) claimContent(absPath string) (ce *contentEntry, leader bool, ok bool) {
	size, hash, hashed := r.contentKey(absPath)
	if !hashed {
		return nil, false, false
	}
	r.contentMu.Lock()
	defer r.contentMu.Unlock()
	if r.contents == nil {
		r.contents = make(map[int64]map[string]*contentEntry)
	}
	bucket, seen := r.contents[size]
	if !seen {
		bucket = make(map[string]*contentEntry)
		r.contents[size] = bucket
	}
	if existing, hit := bucket[hash]; hit {
		return existing, false, true
	}
	ce = &contentEntry{done: make(chan struct{})}
	bucket[hash] = ce
	return ce, true, true
}

func (r *Runner) settleContent(ce *contentEntry, url string, err error) {
	if ce == nil {
		return
	}
	ce.url, ce.err = url, err
	close(ce.done)
}

// contentKey 给要上传的图算大小 + sha256。
//
// 这里每张图都会多读一遍（哈希），换的是「内容相同、路径不同」只传一次：
// 读完到发出去之间还有一整趟 HTTPS 上传，本地这一次读基本被页缓存吃掉，
// 而重复上传要花的是真实的上行带宽与时间。
func (r *Runner) contentKey(absPath string) (size int64, hash string, ok bool) {
	st, err := os.Stat(absPath)
	if err != nil || st.IsDir() || st.Size() <= 0 {
		return 0, "", false
	}
	f, ferr := os.Open(absPath)
	if ferr != nil {
		return 0, "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, cerr := io.Copy(h, f); cerr != nil {
		return 0, "", false
	}
	return st.Size(), fmt.Sprintf("%x", h.Sum(nil)), true
}

// imageSignatures 是几种常见图片格式的开头。用来判断「文件头连签名都不完整」。
var imageSignatures = [][]byte{
	{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
	{0xFF, 0xD8, 0xFF},
	{'G', 'I', 'F', '8'},
	{'R', 'I', 'F', 'F'},
	{'B', 'M'},
	{0x00, 0x00, 0x01, 0x00}, // ICO
	{0x66, 0x74, 0x79, 0x70}, // ISO BMFF（avif/heic 的 ftyp）
}

// unusableImage 判断这张本地图根本没必要传：空文件，或者文件头短到连签名都不完整。
//
// 为什么只查这两种：图床只认 HTTP 请求、不认内容，一张 0 字节或 `\x89PNG` 四个字节的
// 「图片」也能「上传成功」，于是文档里换上一个点开永远打不开的链接，而报告写着
// 「替换 2 处链接」。这两种都无可争议 —— 谁都不需要把半个文件头发出去。
//
// 不查结尾标记（`looksComplete` 那一套）是故意的：认不出的格式（avif/heic/…）
// 一律放行，宁可不拦也不能错杀用户那张奇怪格式的图。SVG 是文本，天然没有二进制签名，
// 也不会命中这些前缀。
func unusableImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return false
	}
	if st.Size() <= 0 {
		return true
	}

	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	head = head[:n]
	for _, sig := range imageSignatures {
		if len(head) < len(sig) && bytes.HasPrefix(sig, head) {
			return true
		}
	}
	return false
}
