package conv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/geekMus/markpic/internal/i18n"
	"github.com/geekMus/markpic/internal/imgpath"
	"github.com/geekMus/markpic/internal/mdscan"
	"github.com/geekMus/markpic/internal/platform"
	"github.com/geekMus/markpic/internal/remoteimg"
	"github.com/geekMus/markpic/internal/settings"
	"github.com/geekMus/markpic/internal/textenc"
)

// filePass 是「处理一篇文档」这一趟的工作台。
//
// 一趟处理分五段：读盘解码 → 扫出引用 → 逐引用分类 → 真正干活 → 计数写回。
// 后一段都要用前一段留下的东西（正文、引用、逐图明细），全塞进这个结构体，
// 阶段之间就不必拖着六七个参数互相传，processFile 本身也就只剩五行。
type filePass struct {
	r    *Runner
	res  FileResult
	path string
	// text 是解码成 UTF-8 的正文，enc 是它原来的编码，写回去时要转回去。
	text string
	enc  textenc.Encoding
	refs []mdscan.Ref
	// items 是逐图明细，与 refs 一一对应。
	items []Item
	// groups 是「同一张本地图被引用多次」攒成的活儿，remotes 是远程那一侧。
	groups     []*itemGroup
	remoteMode string
	// changed 是真正改动了几处，写回时正好当 repls 的容量。
	changed int
	// out 是改写并重新编码后的字节，等着落盘。
	out []byte
}

// processFile 处理单个 Markdown 文件。
func (r *Runner) processFile(ctx context.Context, id, path string) FileResult {
	p := &filePass{
		r:    r,
		path: path,
		res: FileResult{
			ID:    id,
			Path:  path,
			Name:  filepath.Base(path),
			Dir:   filepath.Dir(path),
			Items: []Item{},
		},
	}
	if !p.readSource() {
		return p.res
	}
	if !p.scanRefs() {
		return p.res
	}
	p.planItems()
	p.runPass(ctx, id)
	p.tally()
	if p.reportOnly(ctx) {
		return p.res
	}
	p.writeBack()
	return p.res
}

// readSource 把文件读进来并解码成 UTF-8。失败时 res 已经写好状态，调用方直接返回。
func (p *filePass) readSource() bool {
	res := &p.res
	data, err := os.ReadFile(p.path)
	if err != nil {
		res.Status = FileFailed
		// 「being used by another process」这种系统原话用户得猜，直接说该怎么办。
		if platform.IsBusy(err) {
			res.Error = i18n.T(p.r.lang, i18n.KeyConvReadLocked, p.path)
		} else {
			res.Error = i18n.T(p.r.lang, i18n.KeyConvReadFailed, err)
		}
		return false
	}
	// 记事本存的 UTF-16、老中文文档的本机 ANSI，都在这里先转成 UTF-8 再扫。
	// 扫描和替换靠字节区间，内部只能有一种编码；写回去的时候再转回原编码。
	p.enc = textenc.Detect(data)
	text, derr := textenc.Decode(data, p.enc)
	if derr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(p.r.lang, i18n.KeyConvNotUTF8)
		return false
	}
	p.text = string(text)
	return true
}

// scanRefs 扫出引用，并去掉两类「不该出现在报告里」的写法：
// 贪心解析出来但其实不存在的路径，以及图床渲染不了的附件。
func (p *filePass) scanRefs() bool {
	res, r := &p.res, p.r
	doc := mdscan.ScanLang([]byte(p.text), r.lang)
	res.Warnings = append(res.Warnings, doc.Warnings...)

	// 地址里有空格又没写 <地址> 的写法（CommonMark 不允许，但不少编辑器照样生成）：
	// 只有「贪心解析出来的那个文件」真的存在时才认它，否则当作没看见 ——
	// 免得把正文里的 ![说明](见 上文) 之类也报成「找不到文件」。
	refs := make([]mdscan.Ref, 0, len(doc.Refs))
	for _, ref := range doc.Refs {
		if ref.Loose && !r.looseTargetExists(p.path, ref.Target) {
			continue
		}
		// 附件（.ps/.eps/.pdf 之类）不上传：图床当图片渲染不了它们，传上去也用不了。
		// 只跳本地路径 —— 远程地址本来就原样留着，不该多一条提示。
		if imgpath.Classify(ref.Target) == imgpath.Local && isAttachment(ref.Target) {
			res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvAttachmentSkipped, ref.Line, ref.Target))
			continue
		}
		if ref.Loose {
			res.Warnings = append(res.Warnings, i18n.T(r.lang, i18n.KeyConvLoosePath, ref.Line))
		}
		refs = append(refs, ref)
	}
	p.refs = refs

	res.Images = len(refs)
	if len(refs) == 0 {
		res.Status = FileNoImages
		return false
	}
	return true
}

// planItems 把每一处引用判成「上传 / 下载 / 检测 / 缺文件」，顺手攒出待办的活儿。
//
// 无论成功失败都要把逐图明细带回给调用方（界面展开、命令行提示都靠它）。
func (p *filePass) planItems() {
	res, r := &p.res, p.r
	items := make([]Item, len(p.refs))
	groups := make([]*itemGroup, 0, len(p.refs))
	byPath := make(map[string]*itemGroup, len(p.refs))
	remoteMode := r.remoteMode()

	for i, ref := range p.refs {
		it := Item{Target: ref.Target, Kind: ref.Kind, Line: ref.Line}

		kind := imgpath.Classify(ref.Target)
		// 「把图取回来」的两个模式不碰本地图片：用户在意的正是「反方向」，
		// 顺手把本地图传出去就完全走反了。
		if kind == imgpath.Local && remoteMode != settings.RemoteIgnore {
			// 这两个模式不碰本地图（用户在意的正是反方向），但「引用的本地图
			// 根本不存在」还是得说出来：以前这类引用直接从报告里消失了，一篇
			// 文档缺一张图也照样报「失效 0 张」，看着像一切正常。
			if abs, ok := imgpath.ResolveExisting(p.path, ref.Target, r.fileExists); !ok || !r.fileExists(abs) {
				res.LocalMissing++
				it.Error = i18n.T(r.lang, i18n.KeyConvFileMissing, filepath.Base(ref.Target))
			} else {
				// 记住它到底在哪：输出到别的目录时要把这张图一起搬过去，
				// 不然副本里那条相对链接就断了。
				it.AbsolutePath = abs
			}
			it.Status = StatusLocalKept
			items[i] = it
			continue
		}

		switch kind {
		case imgpath.Remote:
			it.Status = StatusRemote
		case imgpath.Empty:
			it.Status = StatusMissing
			it.Error = i18n.T(r.lang, i18n.KeyConvEmptyTarget)
		default:
			// 先算主候选（正常写法）；它不存在时才算退化写法——
			// 绝大多数引用的第一发就命中，四种候选全算出来只是白花分配。
			abs, ok := imgpath.ResolveExisting(p.path, ref.Target, r.fileExists)
			if !ok {
				it.Status = StatusMissing
				it.Error = i18n.T(r.lang, i18n.KeyConvUnresolvedTarget)
				break
			}
			it.AbsolutePath = abs
			if !r.fileExists(abs) {
				it.Status = StatusMissing
				// 目录不是文件，但「这个文件夹明明在」和「文件不存在」得说清楚区别。
				if st, serr := os.Stat(abs); serr == nil && st.IsDir() {
					it.Error = i18n.T(r.lang, i18n.KeyConvPathIsDir)
				} else {
					it.Error = i18n.T(r.lang, i18n.KeyConvFileMissing, filepath.Base(abs))
				}
				break
			}
			key := strings.ToLower(abs)
			g := byPath[key]
			if g == nil {
				g = &itemGroup{abs: abs}
				byPath[key] = g
				groups = append(groups, g)
			}
			g.refs = append(g.refs, i)
			it.Status = StatusPending
		}
		items[i] = it
	}
	p.items, p.groups, p.remoteMode = items, groups, remoteMode
	res.Items = items
}

// runPass 真正干活：上传本地图、下载远程图、或者只探一下远程地址还在不在。
func (p *filePass) runPass(ctx context.Context, id string) {
	r := p.r
	remotes := []*remoteGroup(nil)
	if p.remoteMode != settings.RemoteIgnore {
		remotes = r.buildRemoteGroups(p.items, p.refs)
	}

	// 预检模式跳过真正的网络动作，待处理项保持 pending。
	//
	// 但「只检测远程图」这一档是纯读的：不发一张图、不改一个字节，检测结果本身
	// 就是用户要的东西 —— 跳过它等于什么都没查。实测 449 篇的真实语料里，加上
	// --dry-run 就只有「无图 67 个」，380 处死链一个都没查出来。
	if r.previewOnly {
		// 预检只扫描计数：远程图保持 status=remote（前端显示「待检测」），
		// 本地图保持 pending，等用户真正点「开始」才发网络请求。
	} else if !r.cfg.DryRun {
		r.runUnits(ctx, id, r.remoteBaseDir(p.path), p.groups, remotes, p.items)
	} else if p.remoteMode == settings.RemoteCheck {
		r.runUnits(ctx, id, r.remoteBaseDir(p.path), nil, remotes, p.items)
	}
}

// tally 把逐图明细数成一份汇总。
//
// 这里的每个「去重后的数」都有同一个来由：一处引用不等于一张图（也不等于一个
// 地址），报告里两个数必须说得通，否则用户会以为图床被敲了十次门。
func (p *filePass) tally() {
	res := &p.res
	// 记「这一篇里到底下成了几个不同的文件」，只为把「20 处引用、1 张图」说清楚。
	var localizedNames map[string]struct{}
	// 检测侧同理：引用 10 次只发 1 个请求，报告里得说得出这两个数的区别。
	var remoteURLs map[string]struct{}
	// 失效那一侧也要去重，否则「失效 3 张」会和「失效的远程地址（1 个）」打架。
	var deadURLs map[string]struct{}
	// 下载失败的地址同样按地址去重：清单按地址列，报告里的数也得按地址说。
	var failedURLs map[string]struct{}
	for _, it := range p.items {
		switch it.Status {
		case StatusUploaded:
			res.Uploaded++
		case StatusReused, StatusCached:
			// 复用也算「这一处替换成功」，但不占上传次数；Reused 单独记账，
			// 界面就不用为了显示「复用几张」再把上万条明细数一遍。
			res.Uploaded++
			res.Reused++
		case StatusLocalized:
			res.Localized++
			// 同一张图被引用 20 次时 Localized 是 20，可 images/ 里只有一个文件。
			// 只报 20 会让人以为另外 19 张失败了，所以另记一个去重后的张数。
			if it.URL != "" {
				if localizedNames == nil {
					localizedNames = make(map[string]struct{}, 8)
				}
				if _, seen := localizedNames[it.URL]; !seen {
					localizedNames[it.URL] = struct{}{}
					res.LocalizedUnique++
				}
			} else {
				res.LocalizedUnique++
			}
		case StatusRemoteOK, StatusRemoteBad:
			res.Checked++
			// 同一张图被引用 10 次时 Checked 是 10，可我们只发了一个请求。
			// 只报 10 会让人以为这个图床被敲了十次门，所以另记去重后的次数。
			key := remoteimg.Key(it.Target)
			if remoteURLs == nil {
				remoteURLs = make(map[string]struct{}, 8)
			}
			if _, seen := remoteURLs[key]; !seen {
				remoteURLs[key] = struct{}{}
				res.CheckedUnique++
			}
			if it.Status == StatusRemoteBad {
				res.Dead++
				// 失效这一侧同样要去重：一个死链被引用 3 次时，「失效 3 张」
				// 和结尾那句「失效的远程地址（1 个）」会当场打架。
				if deadURLs == nil {
					deadURLs = make(map[string]struct{}, 4)
				}
				if _, seen := deadURLs[key]; !seen {
					deadURLs[key] = struct{}{}
					res.DeadUnique++
				}
			}
		case StatusRemote:
			// 碰不了的地址（ftp://、data:）单独计数：它们既不是「检测过」也不是
			// 「失效」，混进 Skipped 里就再也没人看得见了。
			if it.Error != "" {
				res.RemoteUnsupported++
			} else {
				res.Skipped++
				// 单独记一笔「原样保留的远程图」：它同时算在 Skipped 里（界面上
				// 那句「跳过 N」没变），但一篇只有远程图的文档会报「没有可处理的
				// 图片」，不说这句就像文档里一张图都没有。
				res.RemoteKept++
			}
		case StatusFailed, StatusMissing:
			res.Failed++
			// 远程地址没拿到图，和本地图片读不出来是两件事：前者要照着清单去改
			// 文档里的地址，后者要看磁盘。界面上的「复制清单」只该列前者。
			if isRemoteURL(it.Target) {
				res.RemoteFailed++
				// 同一个地址在多处引用时只算一个：报告里那句「没下载回来 N 个地址」
				// 和结尾清单里的地址数必须是同一个数，否则两句话会打架。
				key := remoteimg.Key(it.Target)
				if failedURLs == nil {
					failedURLs = make(map[string]struct{}, 4)
				}
				if _, seen := failedURLs[key]; !seen {
					failedURLs[key] = struct{}{}
					res.RemoteFailedUnique++
				}
			}
		case StatusCancelled:
			// 取消造成的「没传」不是失败，别把它算进失败数里吓人。
			res.Skipped++
		default:
			res.Skipped++
		}
	}
}

// reportOnly 处理所有「不写文件、只出报告」的收尾。
//
// 几种情况各有各的来由（只检测、试运行、被取消、一处都没改成），共同点是
// 文档一个字都不会变：结果在这一步就定稿了，返回真表示调用方直接交付。
func (p *filePass) reportOnly(ctx context.Context) bool {
	res, r := &p.res, p.r

	// 只做检测：一个字都不改，直接把报告带回去（预检里也是这样）。
	if p.remoteMode == settings.RemoteCheck {
		res.Status = FileChecked
		return true
	}

	if r.cfg.DryRun {
		res.Status = FilePreview
		if out, perr := r.planOutput(p.path, false); perr != nil {
			// 试运行不落盘，但「输出位置」本身有问题（选了目录却没填、或者那个
			// 路径其实是个文件）应该当场说清楚，别等按下开始才失败。
			res.Error = perr.Error()
		} else {
			res.OutputPath = out
			r.warnIfOverwritesSource(res, out, p.path)
		}
		return true
	}

	if ctx.Err() != nil {
		res.Status = FileCancelled
		res.Error = i18n.T(r.lang, i18n.KeyConvCancelled)
		return true
	}

	// 「改动了什么」现在有两种来源：上传成功的本地图、下载回本地的远程图。
	p.changed = res.Uploaded + res.Localized
	if p.changed == 0 && res.Dead > 0 {
		// 远程地址全是坏的，没有可写的内容；但这不是「文档里没有图」，得如实说。
		res.Status = FilePartial
		return true
	}
	if p.changed == 0 && res.Failed > 0 {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvAllFailed)
		return true
	}
	if p.changed == 0 {
		res.Status = FileNoImages
		return true
	}
	return false
}

// writeBack 把新地址写回文档，并在写成功后把跟着走的本地图搬过去。
func (p *filePass) writeBack() {
	if !p.applyRewrite() {
		return
	}
	if !p.writeDocument() {
		return
	}

	res := &p.res
	// Dead 是「远程地址本来就打不开」：那种图没能本地化，链接还是原来那个
	// 断掉的地址。文件写出来了，但不是全须全尾 —— 说「完成」会让人以为
	// 这一篇已经收拾干净了，下次再看才发现还有几张是坏的。
	if res.Failed == 0 && res.Dead == 0 {
		res.Status = FileOK
	} else {
		res.Status = FilePartial
	}
}

// applyRewrite 按原有写法替换每一处地址，然后编码回原来那种字节。
//
// 失败时 res 已写好状态；图可能早就传上去了，所以调用方还要看 noteOrphanImages。
func (p *filePass) applyRewrite() bool {
	res, r := &p.res, p.r
	repls := make([]mdscan.Repl, 0, p.changed)
	for i, ref := range p.refs {
		if p.items[i].URL == "" {
			continue
		}
		text := p.items[i].URL
		switch {
		case ref.Reference:
			// 引用式图片（![alt][label]）要改的是标签那一段：换成 (地址)，
			// 于是这一处就变成了等价的整行写法，文档里的定义保持原样。
			//
			// 标题只写在定义里，换写法时必须一起带过来：不然渲染出来的图
			// 少了悬停提示，等于我们替用户删了点东西。
			if ref.Title != "" {
				text = "(" + wrapURL(text) + " " + quoteTitle(ref.Title) + ")"
			} else {
				text = "(" + wrapURL(text) + ")"
			}
		case ref.Kind == mdscan.KindHTML && ref.Quote == '<':
			text = "<" + text + ">"
		case ref.Kind == mdscan.KindMarkdown:
			// 下载回来的图沿用远程地址的文件名，那里面完全可能有空格
			// （`/img/4%20x.png` → `images/4 x.png`）。CommonMark 的地址遇到空格就结束，
			// 不包尖括号的话 `![a](images/4 x.png)` 在渲染器眼里是「地址 images/4
			// + 一个不合法的标题」，图片直接不显示 —— 我们自己写出去的链接得是能用的。
			text = wrapURL(text)
		}
		repls = append(repls, mdscan.Repl{Start: ref.URLStart, End: ref.URLEnd, Text: text})
	}

	out, aerr := mdscan.Apply([]byte(p.text), repls)
	if aerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvRewriteFailed, aerr)
		return false
	}
	// 原样写回原编码：UTF-16 的文档不应该因为我们处理过就变成 UTF-8。
	encoded, eerr := textenc.Encode(out, p.enc)
	if eerr != nil {
		res.Status = FileFailed
		res.Error = i18n.T(r.lang, i18n.KeyConvEncodeFailed, p.enc.String())
		return false
	}
	p.out = encoded
	return true
}

// writeDocument 定下输出路径、必要时备份源文件，然后把新内容落盘。
func (p *filePass) writeDocument() bool {
	res, r := &p.res, p.r
	target, perr := r.planOutput(p.path, true)
	if perr != nil {
		res.Status = FileFailed
		res.Error = perr.Error()
		// 图已经传上去了，可连输出路径都没算出来：文件里一处都没改。
		// 汇总要是还写「替换 N 处链接」，那就是在说一件没发生的事。
		r.noteOrphanImages(res)
		return false
	}

	// 目录模式的输出目录正好就是源文件所在的目录时，输出路径会等于源文件路径 ——
	// 于是「输出到目录」悄悄变成了「原地覆盖」。这不算错（有人就是想要这个效果，
	// 备份也照做），但和用户在界面上选的那个模式不是一回事，得说一声。
	r.warnIfOverwritesSource(res, target, p.path)
	if imgpath.SameFile(target, p.path) && r.cfg.Backup {
		if berr := backupFile(p.path); berr != nil {
			res.Status = FileFailed
			res.Error = i18n.T(r.lang, i18n.KeyConvBackupFailed, berr)
			return false
		}
	}
	if werr := writeOutput(target, p.out); werr != nil {
		res.Status = FileFailed
		// 图已经上传 / 已经下载，可文档没写成 —— 磁盘上那份文件一个字都没变。
		r.noteOrphanImages(res)
		switch {
		case errors.Is(werr, errTargetReadOnly):
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteReadOnly, target)
		case errors.Is(werr, errTargetLocked):
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteLocked, target)
		case errors.Is(werr, errTargetIsDir):
			res.Error = i18n.T(r.lang, i18n.KeyConvTargetIsDir, target)
		default:
			res.Error = i18n.T(r.lang, i18n.KeyConvWriteFailed, werr)
		}
		return false
	}

	res.OutputPath = target
	// 文档写成功了再搬图：写失败时在输出目录里留一堆图片是没意义的。
	r.carryLocalImages(res, p.path, target)
	return true
}
