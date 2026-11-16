package conv

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/geekMus/markpic/internal/platform"
)

func backupFile(path string) error {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // 已有备份就不覆盖，保留最早的原件
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0o644)
}

// writeOutput 把结果写到 path。
//
// 目标不存在时直接创建写：这条路占绝大多数输出，而「临时文件 + 改名」
// 在 Windows 上每次要多花三次系统调用（实测 0.30 ms/个 对 0.08 ms/个，
// 300 个输出就是 60 多毫秒）。O_EXCL 顺带保证了不会覆盖别人刚写的文件。
//
// 目标已存在时必须走临时文件 + 改名：中途失败不能把原文件写坏——原地替换
// 模式下那个「原文件」就是用户的源文档，这一步不能省。
func writeOutput(path string, data []byte) error {
	// 目标位置上蹲着一个同名**目录**时，系统给的话是「open …: is a directory」，
	// 用户看不懂也不知道该动哪个东西（他甚至可能是自己建的目录）。
	if st, serr := os.Stat(path); serr == nil && st.IsDir() {
		return fmt.Errorf("%w: %s", errTargetIsDir, path)
	}
	err := createExclusive(path, data)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		// 内容已经一模一样就别重写：重复跑一次不会白改一遍文件时间戳，
		// 也省掉改名那次最贵的系统调用。
		if same, cerr := sameContent(path, data); cerr == nil && same {
			return nil
		}
		return writeAtomic(path, data)
	case errors.Is(err, fs.ErrNotExist):
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
			return mkErr
		}
		return createExclusive(path, data)
	default:
		return err
	}
}

// sameContent 判断 path 里已有的内容是不是和 data 完全一样。
func sameContent(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Equal(existing, data), nil
}

// createExclusive 用 O_EXCL 新建并写入；失败时不留半截文件。
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		f.Close()
		os.Remove(path)
		return werr
	}
	return f.Close()
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 上次被强杀时留下的临时文件会一直躺在用户的目录里，顺手收拾掉。
	sweepStaleTemps(dir, writeTempPrefix)
	tmp, err := os.CreateTemp(dir, writeTempPrefix+"*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 改名成功之后临时文件就不存在了，这时再去删它是白跑一次系统调用
	// （Windows 上 Remove 一个不存在的路径要花一次 syscall 才报错）。
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows 上覆盖只读文件会得到一句「Access is denied」加临时文件名，
		// 对用户毫无意义；这里换成能直接照做的说法。
		if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o222 == 0 {
			return fmt.Errorf("%w: %s", errTargetReadOnly, path)
		}
		// 文件被别的程序打开着（编辑器、Word、杀毒扫描）时同样是一句
		// 「Access is denied」，但那跟只读是两回事：关掉占用它的程序就行。
		if platform.IsBusy(err) {
			return fmt.Errorf("%w: %s", errTargetLocked, path)
		}
		return err
	}
	renamed = true
	return nil
}

// errTargetReadOnly 表示目标文件被标了只读，覆盖不了。
// 调用方用它换成一句人话（见 processFile）。
var errTargetReadOnly = errors.New("target file is read-only")

// errTargetLocked 表示目标文件被别的程序占着（或没有权限），写不进去。
var errTargetLocked = errors.New("target file is locked")

// errTargetIsDir 表示目标路径上蹲着一个同名目录。
// 调用方用它换成一句人话（见 processFile）。
var errTargetIsDir = errors.New("target path is a directory")
