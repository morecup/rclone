package paths

import (
	"errors"
	errors2 "github.com/pkg/errors"
	"path/filepath"
	"strings"
)

var ExceedsRootDirError = errors.New("path exceeds the root directory")

// A lazybuf is a lazily constructed path buffer.
// It supports append, reading previously appended bytes,
// and retrieving the final string. It does not allocate a buffer
// to hold the output until that output diverges from s.
type lazybuf struct {
	s   string
	buf []byte
	w   int
}

func (b *lazybuf) index(i int) byte {
	if b.buf != nil {
		return b.buf[i]
	}
	return b.s[i]
}

func (b *lazybuf) append(c byte) {
	if b.buf == nil {
		if b.w < len(b.s) && b.s[b.w] == c {
			b.w++
			return
		}
		b.buf = make([]byte, len(b.s))
		copy(b.buf, b.s[:b.w])
	}
	b.buf[b.w] = c
	b.w++
}

func (b *lazybuf) string() string {
	if b.buf == nil {
		return b.s[:b.w]
	}
	return string(b.buf[:b.w])
}

// Join joins any number of path elements into a single path,
// separating them with slashes. Empty elements are ignored.
// The result is Cleaned. However, if the argument list is
// empty or all its elements are empty, Join returns
// an empty string.
func Join(elem ...string) (string, error) {
	size := 0
	for _, e := range elem {
		size += len(e)
	}
	if size == 0 {
		return "", nil
	}
	buf := make([]byte, 0, size+len(elem)-1)
	for _, e := range elem {
		if len(buf) > 0 || e != "" {
			if len(buf) > 0 {
				buf = append(buf, '/')
			}
			buf = append(buf, e...)
		}
	}
	return Clean(string(buf))
}

// Clean returns the shortest path name equivalent to path
// by purely lexical processing. It applies the following rules
// iteratively until no further processing can be done:
//
//  1. Replace multiple slashes with a single slash.
//  2. Eliminate each . path name element (the current directory).
//  3. Eliminate each inner .. path name element (the parent directory)
//     along with the non-.. element that precedes it.
//  4. Eliminate .. elements that begin a rooted path:
//     that is, replace "/.." by "/" at the beginning of a path.
//
// The returned path ends in a slash only if it is the root "/".
//
// If the result of this process is an empty string, Clean
// returns the string ".".
//
// See also Rob Pike, “Lexical File Names in Plan 9 or
// Getting Dot-Dot Right,”
// https://9p.io/sys/doc/lexnames.html
func Clean(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	rooted := path[0] == '/'
	n := len(path)

	// Invariants:
	//	reading from path; r is index of next byte to process.
	//	writing to buf; w is index of next byte to write.
	//	dotdot is index in buf where .. must stop, either because
	//		it is the leading slash or it is a leading ../../.. prefix.
	out := lazybuf{s: path}
	r, dotdot := 0, 0
	if rooted {
		out.append('/')
		r, dotdot = 1, 1
	}

	for r < n {
		switch {
		case path[r] == '/':
			// empty path element
			r++
		case path[r] == '.' && (r+1 == n || path[r+1] == '/'):
			// . element
			r++
		case path[r] == '.' && path[r+1] == '.' && (r+2 == n || path[r+2] == '/'):
			// .. element: remove to last /
			r += 2
			switch {
			case out.w > dotdot:
				// can backtrack
				out.w--
				for out.w > dotdot && out.index(out.w) != '/' {
					out.w--
				}
			case !rooted:
				return "", errors2.Wrap(ExceedsRootDirError, "path:"+path)
			case out.w == dotdot:
				return "", errors2.Wrap(ExceedsRootDirError, "path:"+path)
			}
		default:
			// real path element.
			// add slash if needed
			if rooted && out.w != 1 || !rooted && out.w != 0 {
				out.append('/')
			}
			// copy element
			for ; r < n && path[r] != '/'; r++ {
				out.append(path[r])
			}
		}
	}

	// Turn empty string into "."
	if out.w == 0 {
		return "", nil
	}

	return out.string(), nil
}

// IsAbs reports whether the path is absolute.
func IsAbs(path string) bool {
	// 统一替换为Linux风格的斜杠
	normalizedPath := strings.ReplaceAll(path, "\\", "/")

	// 检查Linux绝对路径（以/开头）
	if strings.HasPrefix(normalizedPath, "/") {
		return true
	}

	// 检查Windows绝对路径（盘符路径）
	if len(normalizedPath) >= 3 {
		// 验证盘符格式（字母 + :/）
		if c := normalizedPath[0]; (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			if normalizedPath[1] == ':' && normalizedPath[2] == '/' {
				return true
			}
		}
	}

	// 检查Windows UNC路径（以//开头）
	if strings.HasPrefix(normalizedPath, "//") {
		return true
	}

	return false
}
func JoinTwoPath(filePath string, relPath string) string {
	// 获取文件所在目录（自动清理路径）
	dir := filepath.Dir(filePath)

	// 拼接目录和相对路径
	result := filepath.Join(dir, relPath)

	return filepath.ToSlash(result)
}
